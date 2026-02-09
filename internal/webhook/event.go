package webhook

import (
	"strings"
	"time"
)

// EventType represents a webhook event type.
type EventType string

const (
	BackendHealthy            EventType = "backend.healthy"
	BackendUnhealthy          EventType = "backend.unhealthy"
	CircuitBreakerStateChange EventType = "circuit_breaker.state_change"
	CanaryStarted             EventType = "canary.started"
	CanaryPaused              EventType = "canary.paused"
	CanaryResumed             EventType = "canary.resumed"
	CanaryPromoted            EventType = "canary.promoted"
	CanaryRolledBack          EventType = "canary.rolled_back"
	CanaryStepAdvanced        EventType = "canary.step_advanced"
	CanaryCompleted           EventType = "canary.completed"
	ConfigReloadSuccess       EventType = "config.reload_success"
	ConfigReloadFailure       EventType = "config.reload_failure"
)

// Event represents a webhook event payload.
type Event struct {
	Type      EventType              `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	RouteID   string                 `json:"route_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// NewEvent creates a new Event with the current timestamp.
func NewEvent(typ EventType, routeID string, data map[string]interface{}) *Event {
	return &Event{
		Type:      typ,
		Timestamp: time.Now(),
		RouteID:   routeID,
		Data:      data,
	}
}

// matchesPattern checks if an event type matches a subscription pattern.
// Supports exact match and wildcard prefix (e.g., "canary.*" matches "canary.started").
// "*" matches everything.
func matchesPattern(eventType EventType, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(string(eventType), prefix+".")
	}
	return string(eventType) == pattern
}
