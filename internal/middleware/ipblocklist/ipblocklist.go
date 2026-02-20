package ipblocklist

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/variables"
	"go.uber.org/zap"
)

// Blocklist manages a dynamic IP blocklist with static entries and feed-based updates.
type Blocklist struct {
	staticNets []*net.IPNet
	action     string

	feedMu   sync.RWMutex
	feedNets []*net.IPNet // combined nets from all feeds

	feeds  []config.IPBlocklistFeed
	ctx    context.Context
	cancel context.CancelFunc

	metrics *BlocklistMetrics
}

// BlocklistMetrics tracks blocklist statistics.
type BlocklistMetrics struct {
	TotalChecks    atomic.Int64
	BlockedHits    atomic.Int64
	LoggedHits     atomic.Int64
	FeedRefreshes  atomic.Int64
	FeedErrors     atomic.Int64
	StaticEntries  int
	FeedEntries    atomic.Int64
}

// BlocklistStatus is the admin API representation.
type BlocklistStatus struct {
	Action         string `json:"action"`
	StaticEntries  int    `json:"static_entries"`
	FeedEntries    int64  `json:"feed_entries"`
	FeedCount      int    `json:"feed_count"`
	TotalChecks    int64  `json:"total_checks"`
	BlockedHits    int64  `json:"blocked_hits"`
	LoggedHits     int64  `json:"logged_hits"`
	FeedRefreshes  int64  `json:"feed_refreshes"`
	FeedErrors     int64  `json:"feed_errors"`
}

// New creates a new Blocklist from config.
func New(cfg config.IPBlocklistConfig) (*Blocklist, error) {
	action := cfg.Action
	if action == "" {
		action = "block"
	}

	staticNets, err := parseEntries(cfg.Static)
	if err != nil {
		return nil, fmt.Errorf("ipblocklist: invalid static entry: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	bl := &Blocklist{
		staticNets: staticNets,
		action:     action,
		feeds:      cfg.Feeds,
		ctx:        ctx,
		cancel:     cancel,
		metrics: &BlocklistMetrics{
			StaticEntries: len(staticNets),
		},
	}

	// Start background refresh for each feed
	for _, feed := range cfg.Feeds {
		go bl.refreshLoop(feed)
	}

	return bl, nil
}

// Check returns true if the IP is blocked.
func (bl *Blocklist) Check(ip net.IP) bool {
	bl.metrics.TotalChecks.Add(1)

	// Check static entries
	for _, n := range bl.staticNets {
		if n.Contains(ip) {
			return true
		}
	}

	// Check feed entries
	bl.feedMu.RLock()
	nets := bl.feedNets
	bl.feedMu.RUnlock()

	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

// Middleware returns a middleware that checks the IP blocklist.
func (bl *Blocklist) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := variables.ExtractClientIP(r)
			ip := net.ParseIP(clientIP)
			if ip == nil {
				next.ServeHTTP(w, r)
				return
			}

			if bl.Check(ip) {
				if bl.action == "log" {
					bl.metrics.LoggedHits.Add(1)
					logging.Warn("IP blocklist match (log mode)",
						zap.String("ip", clientIP),
						zap.String("path", r.URL.Path),
					)
					next.ServeHTTP(w, r)
					return
				}
				bl.metrics.BlockedHits.Add(1)
				errors.ErrForbidden.WithDetails("IP address blocked").WriteJSON(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ForceRefresh triggers an immediate refresh of all feeds.
func (bl *Blocklist) ForceRefresh() {
	for _, feed := range bl.feeds {
		bl.fetchFeed(feed)
	}
}

// Close stops all background goroutines.
func (bl *Blocklist) Close() {
	bl.cancel()
}

// Status returns the admin status snapshot.
func (bl *Blocklist) Status() BlocklistStatus {
	return BlocklistStatus{
		Action:        bl.action,
		StaticEntries: bl.metrics.StaticEntries,
		FeedEntries:   bl.metrics.FeedEntries.Load(),
		FeedCount:     len(bl.feeds),
		TotalChecks:   bl.metrics.TotalChecks.Load(),
		BlockedHits:   bl.metrics.BlockedHits.Load(),
		LoggedHits:    bl.metrics.LoggedHits.Load(),
		FeedRefreshes: bl.metrics.FeedRefreshes.Load(),
		FeedErrors:    bl.metrics.FeedErrors.Load(),
	}
}

// refreshLoop runs a ticker-based refresh loop for a single feed.
func (bl *Blocklist) refreshLoop(feed config.IPBlocklistFeed) {
	// Initial fetch
	bl.fetchFeed(feed)

	interval := feed.RefreshInterval
	if interval == 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-bl.ctx.Done():
			return
		case <-ticker.C:
			bl.fetchFeed(feed)
		}
	}
}

// fetchFeed fetches and parses a single feed, atomically updating the feedNets.
func (bl *Blocklist) fetchFeed(feed config.IPBlocklistFeed) {
	ctx, cancel := context.WithTimeout(bl.ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", feed.URL, nil)
	if err != nil {
		bl.metrics.FeedErrors.Add(1)
		logging.Warn("IP blocklist feed request error", zap.String("url", feed.URL), zap.Error(err))
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		bl.metrics.FeedErrors.Add(1)
		logging.Warn("IP blocklist feed fetch error", zap.String("url", feed.URL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bl.metrics.FeedErrors.Add(1)
		logging.Warn("IP blocklist feed non-200 status",
			zap.String("url", feed.URL),
			zap.Int("status", resp.StatusCode),
		)
		return
	}

	format := feed.Format
	if format == "" {
		format = "text"
	}

	var entries []string
	switch format {
	case "text":
		entries = parseTextFeed(resp.Body)
	case "json":
		if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
			bl.metrics.FeedErrors.Add(1)
			logging.Warn("IP blocklist feed JSON parse error", zap.String("url", feed.URL), zap.Error(err))
			return
		}
	default:
		bl.metrics.FeedErrors.Add(1)
		logging.Warn("IP blocklist feed unknown format", zap.String("url", feed.URL), zap.String("format", format))
		return
	}

	nets, err := parseEntries(entries)
	if err != nil {
		bl.metrics.FeedErrors.Add(1)
		logging.Warn("IP blocklist feed parse error", zap.String("url", feed.URL), zap.Error(err))
		return
	}

	bl.feedMu.Lock()
	bl.feedNets = nets
	bl.metrics.FeedEntries.Store(int64(len(nets)))
	bl.feedMu.Unlock()

	bl.metrics.FeedRefreshes.Add(1)
	logging.Info("IP blocklist feed refreshed",
		zap.String("url", feed.URL),
		zap.Int("entries", len(nets)),
	)
}

// parseTextFeed parses a newline-delimited text feed, skipping comments and empty lines.
func parseTextFeed(r interface{ Read([]byte) (int, error) }) []string {
	var entries []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, line)
	}
	return entries
}

// parseEntries parses a list of IP/CIDR strings into net.IPNet pointers.
// Single IPs are converted to /32 or /128.
func parseEntries(entries []string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Try CIDR first
		if strings.Contains(entry, "/") {
			_, ipNet, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
			nets = append(nets, ipNet)
			continue
		}

		// Single IP
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP %q", entry)
		}
		if ip.To4() != nil {
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)})
		} else {
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
	}
	return nets, nil
}

// MergeIPBlocklistConfig merges per-route overrides onto global config.
// For blocklists, we combine static entries (OR logic) and use per-route action if set.
func MergeIPBlocklistConfig(perRoute, global config.IPBlocklistConfig) config.IPBlocklistConfig {
	merged := global

	// Per-route static entries are additive
	if len(perRoute.Static) > 0 {
		combined := make([]string, 0, len(global.Static)+len(perRoute.Static))
		combined = append(combined, global.Static...)
		combined = append(combined, perRoute.Static...)
		merged.Static = combined
	}

	// Per-route feeds are additive
	if len(perRoute.Feeds) > 0 {
		combined := make([]config.IPBlocklistFeed, 0, len(global.Feeds)+len(perRoute.Feeds))
		combined = append(combined, global.Feeds...)
		combined = append(combined, perRoute.Feeds...)
		merged.Feeds = combined
	}

	// Per-route action overrides global
	if perRoute.Action != "" {
		merged.Action = perRoute.Action
	}

	merged.Enabled = true
	return merged
}

// BlocklistByRoute manages per-route blocklists.
type BlocklistByRoute struct {
	byroute.Manager[*Blocklist]
}

// NewBlocklistByRoute creates a new BlocklistByRoute manager.
func NewBlocklistByRoute() *BlocklistByRoute {
	return &BlocklistByRoute{}
}

// AddRoute creates and registers a blocklist for the given route.
func (m *BlocklistByRoute) AddRoute(routeID string, cfg config.IPBlocklistConfig) error {
	if !cfg.Enabled {
		return nil
	}

	bl, err := New(cfg)
	if err != nil {
		return err
	}

	m.Add(routeID, bl)
	return nil
}

// GetBlocklist returns the blocklist for a route, or nil.
func (m *BlocklistByRoute) GetBlocklist(routeID string) *Blocklist {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *BlocklistByRoute) Stats() map[string]BlocklistStatus {
	return byroute.CollectStats(&m.Manager, func(bl *Blocklist) BlocklistStatus { return bl.Status() })
}

// CloseAll closes all blocklists.
func (m *BlocklistByRoute) CloseAll() {
	m.Range(func(_ string, bl *Blocklist) bool {
		bl.Close()
		return true
	})
}
