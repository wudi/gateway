package loadbalancer

import (
	"math/rand"
	"net/http"
	"strings"
	"sync"

	"github.com/wudi/gateway/config"
)

// TrafficGroup represents a group of backends with a weight
type TrafficGroup struct {
	Name         string
	Weight       int
	Balancer     *RoundRobin
	MatchHeaders map[string]string
}

// WeightedBalancer implements traffic splitting across multiple backend groups
type WeightedBalancer struct {
	groups       []*TrafficGroup
	groupsByName map[string]*TrafficGroup
	totalWeight  int
	sticky       *StickyPolicy
	mu           sync.RWMutex
}

// NewWeightedBalancer creates a new weighted traffic splitting balancer
func NewWeightedBalancer(splits []config.TrafficSplitConfig) *WeightedBalancer {
	wb := &WeightedBalancer{
		groupsByName: make(map[string]*TrafficGroup),
	}

	for _, split := range splits {
		var backends []*Backend
		for _, b := range split.Backends {
			weight := b.Weight
			if weight == 0 {
				weight = 1
			}
			backends = append(backends, &Backend{
				URL:     b.URL,
				Weight:  weight,
				Healthy: true,
			})
		}

		group := &TrafficGroup{
			Name:         split.Name,
			Weight:       split.Weight,
			Balancer:     NewRoundRobin(backends),
			MatchHeaders: split.MatchHeaders,
		}
		wb.groups = append(wb.groups, group)
		wb.groupsByName[split.Name] = group
		wb.totalWeight += split.Weight
	}

	return wb
}

// NewWeightedBalancerWithSticky creates a weighted balancer with sticky session support.
func NewWeightedBalancerWithSticky(splits []config.TrafficSplitConfig, stickyCfg config.StickyConfig) *WeightedBalancer {
	wb := NewWeightedBalancer(splits)
	wb.sticky = NewStickyPolicy(stickyCfg)
	return wb
}

// NextForRequest selects a backend based on request headers and weights
func (wb *WeightedBalancer) NextForRequest(headers map[string]string) *Backend {
	wb.mu.RLock()
	defer wb.mu.RUnlock()

	// Check header-based overrides first
	for _, group := range wb.groups {
		if len(group.MatchHeaders) > 0 && matchAllHeaders(headers, group.MatchHeaders) {
			return group.Balancer.Next()
		}
	}

	// Random weighted selection
	if wb.totalWeight <= 0 {
		return nil
	}

	roll := rand.Intn(wb.totalWeight)
	cumulative := 0
	for _, group := range wb.groups {
		cumulative += group.Weight
		if roll < cumulative {
			return group.Balancer.Next()
		}
	}

	// Fallback to last group
	if len(wb.groups) > 0 {
		return wb.groups[len(wb.groups)-1].Balancer.Next()
	}
	return nil
}

// NextForHTTPRequest selects a backend using sticky policy if available.
// Returns the selected backend and the traffic group name.
func (wb *WeightedBalancer) NextForHTTPRequest(r *http.Request) (*Backend, string) {
	wb.mu.RLock()
	defer wb.mu.RUnlock()

	// Try sticky policy first
	if wb.sticky != nil {
		groupName := wb.sticky.ResolveGroup(r, wb.groups)
		if groupName != "" {
			if group, ok := wb.groupsByName[groupName]; ok {
				return group.Balancer.Next(), groupName
			}
		}
	}

	// Check header-based overrides
	if r != nil {
		headers := make(map[string]string)
		for _, group := range wb.groups {
			for k := range group.MatchHeaders {
				headers[k] = r.Header.Get(k)
			}
		}
		for _, group := range wb.groups {
			if len(group.MatchHeaders) > 0 && matchAllHeaders(headers, group.MatchHeaders) {
				return group.Balancer.Next(), group.Name
			}
		}
	}

	// Random weighted selection
	if wb.totalWeight <= 0 {
		return nil, ""
	}

	roll := rand.Intn(wb.totalWeight)
	cumulative := 0
	for _, group := range wb.groups {
		cumulative += group.Weight
		if roll < cumulative {
			return group.Balancer.Next(), group.Name
		}
	}

	if len(wb.groups) > 0 {
		last := wb.groups[len(wb.groups)-1]
		return last.Balancer.Next(), last.Name
	}
	return nil, ""
}

// HasStickyPolicy returns true if a sticky policy is configured.
func (wb *WeightedBalancer) HasStickyPolicy() bool {
	return wb.sticky != nil
}

// GetStickyPolicy returns the sticky policy (may be nil).
func (wb *WeightedBalancer) GetStickyPolicy() *StickyPolicy {
	return wb.sticky
}

// GetGroupByName returns a traffic group by name.
func (wb *WeightedBalancer) GetGroupByName(name string) *TrafficGroup {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.groupsByName[name]
}

// Next implements Balancer interface (without header context, uses weight-only)
func (wb *WeightedBalancer) Next() *Backend {
	return wb.NextForRequest(nil)
}

// UpdateBackends updates all group backends
func (wb *WeightedBalancer) UpdateBackends(backends []*Backend) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	// Distribute to first group if only one
	if len(wb.groups) > 0 {
		wb.groups[0].Balancer.UpdateBackends(backends)
	}
}

// MarkHealthy marks a backend as healthy across all groups
func (wb *WeightedBalancer) MarkHealthy(url string) {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	for _, g := range wb.groups {
		g.Balancer.MarkHealthy(url)
	}
}

// MarkUnhealthy marks a backend as unhealthy across all groups
func (wb *WeightedBalancer) MarkUnhealthy(url string) {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	for _, g := range wb.groups {
		g.Balancer.MarkUnhealthy(url)
	}
}

// GetBackends returns all backends across all groups
func (wb *WeightedBalancer) GetBackends() []*Backend {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	var all []*Backend
	for _, g := range wb.groups {
		all = append(all, g.Balancer.GetBackends()...)
	}
	return all
}

// HealthyCount returns total healthy backends across all groups
func (wb *WeightedBalancer) HealthyCount() int {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	count := 0
	for _, g := range wb.groups {
		count += g.Balancer.HealthyCount()
	}
	return count
}

// GetGroups returns the traffic groups
func (wb *WeightedBalancer) GetGroups() []*TrafficGroup {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	result := make([]*TrafficGroup, len(wb.groups))
	copy(result, wb.groups)
	return result
}

// SetGroupWeights atomically sets all group weights and recalculates totalWeight.
// Returns true if the update was applied. Unknown group names are ignored.
func (wb *WeightedBalancer) SetGroupWeights(weights map[string]int) bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	total := 0
	for _, group := range wb.groups {
		if w, ok := weights[group.Name]; ok {
			group.Weight = w
		}
		total += group.Weight
	}
	wb.totalWeight = total
	return true
}

// GetGroupWeights returns a snapshot of current group weights.
func (wb *WeightedBalancer) GetGroupWeights() map[string]int {
	wb.mu.RLock()
	defer wb.mu.RUnlock()

	result := make(map[string]int, len(wb.groups))
	for _, group := range wb.groups {
		result[group.Name] = group.Weight
	}
	return result
}

func matchAllHeaders(requestHeaders, required map[string]string) bool {
	for key, val := range required {
		reqVal, ok := requestHeaders[key]
		if !ok {
			return false
		}
		if !strings.EqualFold(reqVal, val) {
			return false
		}
	}
	return true
}
