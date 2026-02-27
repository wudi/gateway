package pubsub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestPubSubHandlerValidation(t *testing.T) {
	_, err := New(config.PubSubConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestPubSubByRoute(t *testing.T) {
	m := NewPubSubByRoute()
	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats map")
	}
}

func TestNewPublishOnly(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://test-topic",
	})
	if err != nil {
		t.Fatalf("New publish-only failed: %v", err)
	}
	defer h.Close()

	if h.publishTopic == nil {
		t.Error("publishTopic should be non-nil")
	}
	if h.subscription != nil {
		t.Error("subscription should be nil for publish-only")
	}
}

func TestNewBoth(t *testing.T) {
	// mem:// subscription requires topic to exist first, so use same URL for both
	h, err := New(config.PubSubConfig{
		PublishURL:      "mem://test-topic-both",
		SubscriptionURL: "mem://test-topic-both",
	})
	if err != nil {
		t.Fatalf("New both failed: %v", err)
	}
	defer h.Close()

	if h.publishTopic == nil {
		t.Error("publishTopic should be non-nil")
	}
	if h.subscription == nil {
		t.Error("subscription should be non-nil")
	}
}

func TestPublish(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://test-publish",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer h.Close()

	body := `{"message": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "published" {
		t.Errorf("response status = %v, want published", resp["status"])
	}

	stats := h.Stats()
	if stats["total_requests"].(int64) != 1 {
		t.Errorf("total_requests = %v, want 1", stats["total_requests"])
	}
	if stats["published"].(int64) != 1 {
		t.Errorf("published = %v, want 1", stats["published"])
	}
}

func TestPublishWithoutTopic(t *testing.T) {
	// Create a handler with only subscription (need topic first for mem://)
	// Instead, test the path directly by creating a handler with nil publishTopic
	h := &Handler{
		subscriptionURL: "test",
	}
	// subscription is non-nil concept but publishTopic is nil

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("data"))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	if h.totalErrors.Load() != 1 {
		t.Errorf("total_errors = %d, want 1", h.totalErrors.Load())
	}
}

func TestSubscribeWithoutSubscription(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://test-pub-only",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	stats := h.Stats()
	if stats["total_errors"].(int64) != 1 {
		t.Errorf("total_errors = %v, want 1", stats["total_errors"])
	}
}

func TestPublishAndSubscribe(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL:      "mem://test-roundtrip",
		SubscriptionURL: "mem://test-roundtrip",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer h.Close()

	// Publish
	pubReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("round-trip-message"))
	pubW := httptest.NewRecorder()
	h.ServeHTTP(pubW, pubReq)

	if pubW.Code != http.StatusAccepted {
		t.Fatalf("publish status = %d, want %d", pubW.Code, http.StatusAccepted)
	}

	// Subscribe
	subReq := httptest.NewRequest(http.MethodGet, "/", nil)
	subW := httptest.NewRecorder()
	h.ServeHTTP(subW, subReq)

	if subW.Code != http.StatusOK {
		t.Fatalf("subscribe status = %d, want %d", subW.Code, http.StatusOK)
	}

	body, _ := io.ReadAll(subW.Body)
	if string(body) != "round-trip-message" {
		t.Errorf("subscribed body = %q, want %q", body, "round-trip-message")
	}

	stats := h.Stats()
	if stats["published"].(int64) != 1 {
		t.Errorf("published = %v, want 1", stats["published"])
	}
	if stats["consumed"].(int64) != 1 {
		t.Errorf("consumed = %v, want 1", stats["consumed"])
	}
}

func TestPubSubByRouteAddAndGet(t *testing.T) {
	m := NewPubSubByRoute()
	err := m.AddRoute("route-1", config.PubSubConfig{
		PublishURL: "mem://byroute-test",
	})
	if err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	h := m.Lookup("route-1")
	if h == nil {
		t.Fatal("GetHandler returned nil for existing route")
	}

	h2 := m.Lookup("nonexistent")
	if h2 != nil {
		t.Error("GetHandler should return nil for nonexistent route")
	}

	stats := m.Stats()
	if stats == nil {
		t.Error("Stats should not be nil")
	}

	h.Close()
}

func TestStats(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://stats-test",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer h.Close()

	stats := h.Stats()
	if stats["publish_url"] != "mem://stats-test" {
		t.Errorf("publish_url = %v, want mem://stats-test", stats["publish_url"])
	}
	if stats["total_requests"].(int64) != 0 {
		t.Errorf("total_requests = %v, want 0", stats["total_requests"])
	}
}

func TestClose(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL:      "mem://close-test",
		SubscriptionURL: "mem://close-test",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := h.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestClosePublishOnly(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://close-pub-only",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestPUTMethodPublishes(t *testing.T) {
	h, err := New(config.PubSubConfig{
		PublishURL: "mem://put-test",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer h.Close()

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("PUT should publish; status = %d, want %d", w.Code, http.StatusAccepted)
	}
}
