package loadbalancer

import (
	"math/rand"
	"strings"
	"sync"

	"github.com/example/gateway/internal/config"
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
	groups    []*TrafficGroup
	totalWeight int
	mu        sync.RWMutex
}

// NewWeightedBalancer creates a new weighted traffic splitting balancer
func NewWeightedBalancer(splits []config.TrafficSplitConfig) *WeightedBalancer {
	wb := &WeightedBalancer{}

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
		wb.totalWeight += split.Weight
	}

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
