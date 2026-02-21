package mirror

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestNewDiffConfig(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{
		IgnoreHeaders:    []string{"X-Custom", "Authorization"},
		IgnoreJSONFields: []string{"timestamp", "request_id"},
		MaxBodyCapture:   2048,
	})

	if dc.maxBodyCapture != 2048 {
		t.Errorf("expected maxBodyCapture 2048, got %d", dc.maxBodyCapture)
	}
	if !dc.ignoreHeaders["x-custom"] {
		t.Error("expected x-custom in ignore set")
	}
	if !dc.ignoreHeaders["authorization"] {
		t.Error("expected authorization in ignore set")
	}
	// Always-ignored headers
	if !dc.ignoreHeaders["date"] {
		t.Error("expected date in ignore set")
	}
	if !dc.ignoreHeaders["x-request-id"] {
		t.Error("expected x-request-id in ignore set")
	}
}

func TestNewDiffConfig_DefaultMaxBodyCapture(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})

	if dc.maxBodyCapture != defaultMaxBodyCapture {
		t.Errorf("expected default maxBodyCapture %d, got %d", defaultMaxBodyCapture, dc.maxBodyCapture)
	}
}

func TestDiffDetail_DiffTypes(t *testing.T) {
	d := &DiffDetail{
		StatusDiff:  &StatusDiff{PrimaryStatus: 200, MirrorStatus: 500},
		HeaderDiffs: []HeaderDiff{{Header: "X-Foo"}},
		BodyDiffs:   []BodyDiff{{Type: "json_field_diff"}},
	}

	types := d.DiffTypes()
	if len(types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(types))
	}
	if types[0] != "status" || types[1] != "headers" || types[2] != "body" {
		t.Errorf("unexpected types: %v", types)
	}
}

func TestDiffDetail_HasDiffs(t *testing.T) {
	empty := &DiffDetail{}
	if empty.HasDiffs() {
		t.Error("empty detail should not have diffs")
	}

	withStatus := &DiffDetail{StatusDiff: &StatusDiff{}}
	if !withStatus.HasDiffs() {
		t.Error("status diff should be detected")
	}
}

func TestCompareMirrorResponseDetailed_StatusDiff(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})
	body := []byte(`{"ok":true}`)

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       body,
		BodyHash:   sha256.Sum256(body),
	}

	mirrorResp := &http.Response{
		StatusCode: 500,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if result.StatusMatch {
		t.Error("status should not match")
	}
	if !result.BodyMatch {
		t.Error("body should match")
	}
	if detail.StatusDiff == nil {
		t.Fatal("expected status diff")
	}
	if detail.StatusDiff.PrimaryStatus != 200 || detail.StatusDiff.MirrorStatus != 500 {
		t.Errorf("status diff values wrong: %+v", detail.StatusDiff)
	}
}

func TestCompareMirrorResponseDetailed_HeaderDiff(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{
		IgnoreHeaders: []string{"X-Ignored"},
	})
	body := []byte(`test`)

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{"X-Custom": {"a"}, "X-Ignored": {"ignore-me"}},
		Body:       body,
		BodyHash:   sha256.Sum256(body),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Custom": {"b"}, "X-Ignored": {"different"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	_, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if len(detail.HeaderDiffs) != 1 {
		t.Fatalf("expected 1 header diff, got %d", len(detail.HeaderDiffs))
	}
	if detail.HeaderDiffs[0].Header != "X-Custom" {
		t.Errorf("expected X-Custom diff, got %s", detail.HeaderDiffs[0].Header)
	}
	if detail.HeaderDiffs[0].PrimaryValue != "a" || detail.HeaderDiffs[0].MirrorValue != "b" {
		t.Errorf("header diff values wrong: %+v", detail.HeaderDiffs[0])
	}
}

func TestCompareMirrorResponseDetailed_JSONBodyDiff(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{
		IgnoreJSONFields: []string{"timestamp"},
	})

	primaryBody := []byte(`{"name":"alice","age":30,"timestamp":"2024-01-01"}`)
	mirrorBody := []byte(`{"name":"bob","age":30,"timestamp":"2024-01-02"}`)

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       primaryBody,
		BodyHash:   sha256.Sum256(primaryBody),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(mirrorBody)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if result.BodyMatch {
		t.Error("body should not match")
	}

	// Should have a size_diff (different sizes) and json_field_diff for "name"
	// but NOT for "timestamp" (ignored) or "age" (same)
	foundNameDiff := false
	for _, bd := range detail.BodyDiffs {
		if bd.Type == "json_field_diff" {
			if field, ok := bd.Details["field"]; ok && field == "name" {
				foundNameDiff = true
			}
			if field, ok := bd.Details["field"]; ok && field == "timestamp" {
				t.Error("timestamp should be ignored")
			}
			if field, ok := bd.Details["field"]; ok && field == "age" {
				t.Error("age should not differ")
			}
		}
	}
	if !foundNameDiff {
		t.Error("expected name field diff")
	}
}

func TestCompareMirrorResponseDetailed_IdenticalResponses(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})
	body := []byte(`{"status":"ok"}`)

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       body,
		BodyHash:   sha256.Sum256(body),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if !result.StatusMatch || !result.BodyMatch {
		t.Error("identical responses should match")
	}
	if detail.HasDiffs() {
		t.Errorf("expected no diffs, got: %+v", detail)
	}
}

func TestCompareMirrorResponseDetailed_NonJSONBody(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})

	primaryBody := []byte("hello world")
	mirrorBody := []byte("goodbye world")

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       primaryBody,
		BodyHash:   sha256.Sum256(primaryBody),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(mirrorBody)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if result.BodyMatch {
		t.Error("body should not match")
	}

	foundContentDiff := false
	for _, bd := range detail.BodyDiffs {
		if bd.Type == "content_diff" {
			foundContentDiff = true
		}
	}
	if !foundContentDiff {
		t.Error("expected content_diff for non-JSON body")
	}
}

func TestCompareMirrorResponseDetailed_TruncatedFallback(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{
		MaxBodyCapture: 5,
	})

	primaryBody := []byte("this is a long primary body")
	mirrorBody := []byte("this is a long mirror body!")

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       primaryBody[:5], // Only first 5 bytes captured
		BodyHash:   sha256.Sum256(primaryBody),
		Truncated:  true,
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(mirrorBody)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if result.BodyMatch {
		t.Error("body should not match")
	}

	foundHashMismatch := false
	for _, bd := range detail.BodyDiffs {
		if bd.Type == "hash_mismatch" {
			foundHashMismatch = true
			if _, ok := bd.Details["truncated"]; !ok {
				t.Error("hash_mismatch should include truncated flag")
			}
		}
	}
	if !foundHashMismatch {
		t.Error("expected hash_mismatch for truncated bodies")
	}
}

func TestCompareMirrorResponseDetailed_EmptyBody(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})

	primaryBody := []byte("")
	mirrorBody := []byte(`{"data":"value"}`)

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       primaryBody,
		BodyHash:   sha256.Sum256(primaryBody),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(mirrorBody)),
	}

	result, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if result.BodyMatch {
		t.Error("body should not match")
	}

	foundSizeDiff := false
	for _, bd := range detail.BodyDiffs {
		if bd.Type == "size_diff" {
			foundSizeDiff = true
		}
	}
	if !foundSizeDiff {
		t.Error("expected size_diff for empty vs non-empty body")
	}
}

func TestCompareMirrorResponseDetailed_HeaderOnlyInMirror(t *testing.T) {
	dc := NewDiffConfig(config.MirrorCompareConfig{})
	body := []byte("test")

	primary := &PrimaryDiffResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       body,
		BodyHash:   sha256.Sum256(body),
	}

	mirrorResp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Extra": {"mirror-only"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	_, detail := CompareMirrorResponseDetailed(primary, mirrorResp, dc)

	if len(detail.HeaderDiffs) != 1 {
		t.Fatalf("expected 1 header diff, got %d", len(detail.HeaderDiffs))
	}
	if detail.HeaderDiffs[0].Header != "X-Extra" {
		t.Errorf("expected X-Extra diff, got %s", detail.HeaderDiffs[0].Header)
	}
	if detail.HeaderDiffs[0].PrimaryValue != "" {
		t.Error("primary value should be empty for mirror-only header")
	}
}
