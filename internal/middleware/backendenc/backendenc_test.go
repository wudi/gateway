package backendenc

import (
	"encoding/json"
	"testing"

	"github.com/wudi/runway/config"
)

func TestXMLToJSON_Simple(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	xmlData := []byte(`<response><name>alice</name><age>30</age></response>`)
	result, ok := enc.Decode(xmlData, "application/xml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse JSON result: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	// age should be parsed as number
	if data["age"] != float64(30) {
		t.Errorf("expected age=30, got %v (%T)", data["age"], data["age"])
	}
}

func TestXMLToJSON_NestedObjects(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	xmlData := []byte(`<response><user><name>alice</name><email>a@b.com</email></user></response>`)
	result, ok := enc.Decode(xmlData, "text/xml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	user, ok := data["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected user to be object, got %T", data["user"])
	}
	if user["name"] != "alice" {
		t.Errorf("expected user.name=alice, got %v", user["name"])
	}
}

func TestXMLToJSON_Arrays(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	// Repeated elements become arrays
	xmlData := []byte(`<response><item>a</item><item>b</item><item>c</item></response>`)
	result, ok := enc.Decode(xmlData, "application/xml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	items, ok := data["item"].([]interface{})
	if !ok {
		t.Fatalf("expected item to be array, got %T", data["item"])
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
	if items[0] != "a" || items[1] != "b" || items[2] != "c" {
		t.Errorf("unexpected items: %v", items)
	}
}

func TestXMLToJSON_Attributes(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	xmlData := []byte(`<item id="42" active="true"><name>widget</name></item>`)
	result, ok := enc.Decode(xmlData, "application/xml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	// Attributes prefixed with @
	if data["@id"] != float64(42) {
		t.Errorf("expected @id=42, got %v", data["@id"])
	}
	if data["@active"] != true {
		t.Errorf("expected @active=true, got %v", data["@active"])
	}
	if data["name"] != "widget" {
		t.Errorf("expected name=widget, got %v", data["name"])
	}
}

func TestXMLToJSON_EmptyElement(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	xmlData := []byte(`<response><empty></empty><name>test</name></response>`)
	result, ok := enc.Decode(xmlData, "application/xml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if data["empty"] != "" {
		t.Errorf("expected empty string for empty element, got %v (%T)", data["empty"], data["empty"])
	}
}

func TestYAMLToJSON(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "yaml"})

	yamlData := []byte("name: alice\nage: 30\nitems:\n  - one\n  - two\n")
	result, ok := enc.Decode(yamlData, "application/x-yaml")
	if !ok {
		t.Fatal("expected successful decode")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if data["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", data["name"])
	}
	// YAML numbers are uint64 or float64 depending on parser
	items, ok := data["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items to be array, got %T", data["items"])
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestDecodingError_Passthrough(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	invalidXML := []byte(`not valid xml at all`)
	result, ok := enc.Decode(invalidXML, "application/xml")
	if ok {
		t.Error("expected decode failure")
	}

	// Should return original data
	if string(result) != "not valid xml at all" {
		t.Errorf("expected original data on error, got %s", result)
	}

	// Error counter should increment
	stats := enc.Stats()
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %d", stats.Errors)
	}
}

func TestWrongContentType(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	// JSON content type should not be decoded
	data := []byte(`{"already":"json"}`)
	result, ok := enc.Decode(data, "application/json")
	if ok {
		t.Error("expected no decode for JSON content type")
	}
	if string(result) != `{"already":"json"}` {
		t.Error("expected original data returned")
	}
}

func TestByRoute(t *testing.T) {
	br := NewEncoderByRoute()

	br.AddRoute("route1", config.BackendEncodingConfig{Encoding: "xml"})
	br.AddRoute("route2", config.BackendEncodingConfig{Encoding: "yaml"})

	e1 := br.GetEncoder("route1")
	if e1 == nil || e1.Encoding() != "xml" {
		t.Error("expected XML encoder for route1")
	}

	e2 := br.GetEncoder("route2")
	if e2 == nil || e2.Encoding() != "yaml" {
		t.Error("expected YAML encoder for route2")
	}

	e3 := br.GetEncoder("route3")
	if e3 != nil {
		t.Error("expected nil for non-existent route")
	}

	ids := br.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := br.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
}

func TestStats(t *testing.T) {
	enc := New(config.BackendEncodingConfig{Encoding: "xml"})

	xmlData := []byte(`<root><key>value</key></root>`)
	enc.Decode(xmlData, "application/xml")
	enc.Decode(xmlData, "application/xml")

	s := enc.Stats()
	if s.Encoding != "xml" {
		t.Errorf("expected encoding=xml, got %s", s.Encoding)
	}
	if s.Encoded != 2 {
		t.Errorf("expected encoded=2, got %d", s.Encoded)
	}
}
