package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/wudi/runway/config"
)

// deliver sends a single webhook HTTP request to the endpoint.
func (d *Dispatcher) deliver(ep config.WebhookEndpoint, event *Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, ep.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Standard headers
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", string(event.Type))
	req.Header.Set("X-Webhook-Timestamp", timestamp)

	// HMAC-SHA256 signature when secret is configured
	if ep.Secret != "" {
		sig := signPayload(ep.Secret, payload)
		req.Header.Set("X-Webhook-Signature", "sha256="+sig)
	}

	// Custom headers from endpoint config
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// 5xx = retryable
	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error: status %d", resp.StatusCode)
	}

	// 4xx = not retryable, but still counts as failed
	return fmt.Errorf("client error: status %d", resp.StatusCode)
}

// signPayload computes HMAC-SHA256 of the payload using the given secret.
func signPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
