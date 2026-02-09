package transform

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

func TestCompiledBodyTransform_SetFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		SetFields: map[string]string{
			"metadata.source":    "gateway",
			"metadata.timestamp": "$time_unix",
		},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice"}`)
	req := httptest.NewRequest("GET", "/test", nil)
	varCtx := variables.NewContext(req)

	result := ct.Transform(body, varCtx)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("expected metadata to be an object")
	}
	if meta["source"] != "gateway" {
		t.Errorf("expected metadata.source=gateway, got %v", meta["source"])
	}
	if meta["timestamp"] == nil || meta["timestamp"] == "" {
		t.Error("expected metadata.timestamp to be resolved")
	}
	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
}

func TestCompiledBodyTransform_AllowFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AllowFields: []string{"name", "email"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice","email":"a@b.com","password":"secret","age":30}`)
	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	if data["email"] != "a@b.com" {
		t.Errorf("expected email=a@b.com, got %v", data["email"])
	}
	if _, ok := data["password"]; ok {
		t.Error("expected password to be filtered out")
	}
	if _, ok := data["age"]; ok {
		t.Error("expected age to be filtered out")
	}
}

func TestCompiledBodyTransform_DenyFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		DenyFields: []string{"password", "internal_id"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice","password":"secret","internal_id":"xyz","email":"a@b.com"}`)
	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	if data["email"] != "a@b.com" {
		t.Errorf("expected email=a@b.com, got %v", data["email"])
	}
	if _, ok := data["password"]; ok {
		t.Error("expected password to be removed")
	}
	if _, ok := data["internal_id"]; ok {
		t.Error("expected internal_id to be removed")
	}
}

func TestCompiledBodyTransform_Template(t *testing.T) {
	cfg := config.BodyTransformConfig{
		Template: `{"data": {{.body | json}}, "meta": {"request_id": "{{.vars.request_id}}"}}`,
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice"}`)
	req := httptest.NewRequest("GET", "/test", nil)
	varCtx := variables.NewContext(req)
	varCtx.RequestID = "req-123"

	result := ct.Transform(body, varCtx)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	dataObj, ok := data["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}
	if dataObj["name"] != "alice" {
		t.Errorf("expected data.name=alice, got %v", dataObj["name"])
	}

	meta, ok := data["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta to be an object")
	}
	if meta["request_id"] != "req-123" {
		t.Errorf("expected meta.request_id=req-123, got %v", meta["request_id"])
	}
}

func TestCompiledBodyTransform_ProcessingOrder(t *testing.T) {
	// Test that deny is applied before set, and set before add, etc.
	cfg := config.BodyTransformConfig{
		DenyFields:   []string{"secret"},
		SetFields:    map[string]string{"meta.source": "gateway"},
		AddFields:    map[string]string{"added": "yes"},
		RemoveFields: []string{"remove_me"},
		RenameFields: map[string]string{"old": "new"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"secret":"xyz","remove_me":"gone","old":"value","keep":"this"}`)
	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if _, ok := data["secret"]; ok {
		t.Error("expected secret to be denied")
	}
	if _, ok := data["remove_me"]; ok {
		t.Error("expected remove_me to be removed")
	}
	if _, ok := data["old"]; ok {
		t.Error("expected old to be renamed")
	}
	if data["new"] != "value" {
		t.Errorf("expected new=value, got %v", data["new"])
	}
	if data["added"] != "yes" {
		t.Errorf("expected added=yes, got %v", data["added"])
	}
	if data["keep"] != "this" {
		t.Errorf("expected keep=this, got %v", data["keep"])
	}
	meta, ok := data["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta to be an object")
	}
	if meta["source"] != "gateway" {
		t.Errorf("expected meta.source=gateway, got %v", meta["source"])
	}
}

func TestCompiledBodyTransform_BackwardCompat(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AddFields:    map[string]string{"role": "admin"},
		RemoveFields: []string{"age"},
		RenameFields: map[string]string{"name": "username"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice","age":30}`)
	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["role"] != "admin" {
		t.Errorf("expected role=admin, got %v", data["role"])
	}
	if _, ok := data["age"]; ok {
		t.Error("expected age to be removed")
	}
	if _, ok := data["name"]; ok {
		t.Error("expected name to be renamed")
	}
	if data["username"] != "alice" {
		t.Errorf("expected username=alice, got %v", data["username"])
	}
}

func TestCompiledBodyTransform_RemoveNested(t *testing.T) {
	cfg := config.BodyTransformConfig{
		RemoveFields: []string{"internal.secret", "debug"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`{"name":"alice","internal":{"secret":"xyz","id":"123"},"debug":true}`)
	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	internal, ok := data["internal"].(map[string]interface{})
	if !ok {
		t.Fatal("expected internal to remain an object")
	}
	if _, ok := internal["secret"]; ok {
		t.Error("expected internal.secret to be removed")
	}
	if internal["id"] != "123" {
		t.Errorf("expected internal.id=123, got %v", internal["id"])
	}
	if _, ok := data["debug"]; ok {
		t.Error("expected debug to be removed")
	}
}

func TestCompiledBodyTransform_InvalidJSON(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AddFields: map[string]string{"key": "val"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := []byte(`not json`)
	result := ct.Transform(body, nil)

	if !bytes.Equal(result, body) {
		t.Errorf("expected original body returned for invalid JSON")
	}
}

func TestCompiledBodyTransform_InferType(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"42", int64(42)},
		{"3.14", 3.14},
		{"hello", "hello"},
		{"", ""},
	}

	for _, tt := range tests {
		result := inferType(tt.input)
		if result != tt.expected {
			t.Errorf("inferType(%q) = %v (%T), want %v (%T)", tt.input, result, result, tt.expected, tt.expected)
		}
	}
}

func TestCompiledBodyTransform_TransformRequest(t *testing.T) {
	cfg := config.BodyTransformConfig{
		SetFields:    map[string]string{"meta.source": "gateway"},
		RemoveFields: []string{"debug"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := `{"name":"alice","debug":true}`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	varCtx := variables.NewContext(req)
	ct.TransformRequest(req, varCtx)

	result, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	if _, ok := data["debug"]; ok {
		t.Error("expected debug to be removed")
	}
	meta, ok := data["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta to be an object")
	}
	if meta["source"] != "gateway" {
		t.Errorf("expected meta.source=gateway, got %v", meta["source"])
	}
}

func TestCompiledBodyTransform_TransformRequest_NonJSON(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AddFields: map[string]string{"key": "val"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	body := `plain text body`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")

	ct.TransformRequest(req, nil)

	result, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if string(result) != body {
		t.Errorf("expected original body for non-JSON content type, got %q", string(result))
	}
}

func TestCompiledBodyTransform_ResponseMiddleware(t *testing.T) {
	cfg := config.BodyTransformConfig{
		DenyFields: []string{"secret"},
		SetFields:  map[string]string{"meta.served_by": "gateway"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"name":"alice","secret":"xyz"}`))
	})

	mw := ResponseBodyTransformMiddleware(ct)
	handler := mw(backend)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	if _, ok := data["secret"]; ok {
		t.Error("expected secret to be removed")
	}
	meta, ok := data["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta to be an object")
	}
	if meta["served_by"] != "gateway" {
		t.Errorf("expected meta.served_by=gateway, got %v", meta["served_by"])
	}
}

func TestCompiledBodyTransform_EmptyBody(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AddFields: map[string]string{"key": "val"},
	}

	ct, err := NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	result := ct.Transform([]byte{}, nil)
	if len(result) != 0 {
		t.Errorf("expected empty body to be returned as-is, got %q", string(result))
	}
}

func TestCompiledBodyTransform_IsActive(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.BodyTransformConfig
		active bool
	}{
		{"empty", config.BodyTransformConfig{}, false},
		{"add_fields", config.BodyTransformConfig{AddFields: map[string]string{"k": "v"}}, true},
		{"remove_fields", config.BodyTransformConfig{RemoveFields: []string{"k"}}, true},
		{"rename_fields", config.BodyTransformConfig{RenameFields: map[string]string{"a": "b"}}, true},
		{"set_fields", config.BodyTransformConfig{SetFields: map[string]string{"a.b": "c"}}, true},
		{"allow_fields", config.BodyTransformConfig{AllowFields: []string{"a"}}, true},
		{"deny_fields", config.BodyTransformConfig{DenyFields: []string{"a"}}, true},
		{"template", config.BodyTransformConfig{Template: "{{.body}}"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg.IsActive() != tt.active {
				t.Errorf("IsActive() = %v, want %v", tt.cfg.IsActive(), tt.active)
			}
		})
	}
}

func TestCompiledBodyTransform_InvalidTemplate(t *testing.T) {
	cfg := config.BodyTransformConfig{
		Template: "{{.invalid",
	}

	_, err := NewCompiledBodyTransform(cfg)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}
