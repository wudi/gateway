package thrift

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	athrift "github.com/apache/thrift/lib/go/thrift"
	"github.com/cloudwego/thriftgo/parser"
)

func newTestCodec(t *testing.T) (*codec, *serviceSchema) {
	t.Helper()
	cache := newSchemaCache()
	schema, err := cache.getServiceSchemaFromContent("test.thrift", testIDL, "UserService")
	if err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}
	c := &codec{
		structs:  schema.structs,
		enums:    schema.enums,
		typedefs: schema.typedefs,
	}
	return c, schema
}

func newMemoryProtocol() (athrift.TProtocol, *athrift.TMemoryBuffer) {
	buf := athrift.NewTMemoryBuffer()
	prot := athrift.NewTBinaryProtocolConf(buf, nil)
	return prot, buf
}

// fieldType finds a field's Type by name from the User struct in the test IDL.
func fieldType(schema *serviceSchema, structName, fieldName string) *parser.Type {
	s := schema.structs[structName]
	if s == nil {
		return nil
	}
	for _, f := range s.Fields {
		if f.Name == fieldName {
			return f.Type
		}
	}
	return nil
}

func TestCodecWriteReadBaseTypes(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		fieldName string // field in User struct
		value     interface{}
		check     func(t *testing.T, result interface{})
	}{
		{
			name:      "bool_true",
			fieldName: "active",
			value:     true,
			check:     func(t *testing.T, r interface{}) { assertEqual(t, r, true) },
		},
		{
			name:      "bool_false",
			fieldName: "active",
			value:     false,
			check:     func(t *testing.T, r interface{}) { assertEqual(t, r, false) },
		},
		{
			name:      "i32",
			fieldName: "age",
			value:     float64(42),
			check:     func(t *testing.T, r interface{}) { assertEqual(t, r, float64(42)) },
		},
		{
			name:      "i64",
			fieldName: "created_at",
			value:     float64(123456789),
			check:     func(t *testing.T, r interface{}) { assertEqual(t, r, float64(123456789)) },
		},
		{
			name:      "double",
			fieldName: "rating",
			value:     float64(3.14),
			check:     func(t *testing.T, r interface{}) { assertFloat(t, r, 3.14) },
		},
		{
			name:      "string",
			fieldName: "name",
			value:     "hello world",
			check:     func(t *testing.T, r interface{}) { assertEqual(t, r, "hello world") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prot, _ := newMemoryProtocol()
			ft := fieldType(schema, "User", tt.fieldName)
			if ft == nil {
				t.Fatalf("field %s not found", tt.fieldName)
			}

			if err := c.writeValue(ctx, prot, ft, tt.value); err != nil {
				t.Fatalf("writeValue failed: %v", err)
			}

			result, err := c.readValue(ctx, prot, ft)
			if err != nil {
				t.Fatalf("readValue failed: %v", err)
			}

			tt.check(t, result)
		})
	}
}

func TestCodecBinary(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	original := []byte("binary data here")
	b64 := base64.StdEncoding.EncodeToString(original)
	ft := fieldType(schema, "User", "avatar")

	if err := c.writeValue(ctx, prot, ft, b64); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, ft)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(resultStr)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(decoded) != string(original) {
		t.Errorf("binary roundtrip failed: got %q, want %q", decoded, original)
	}
}

func TestCodecList(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ft := fieldType(schema, "User", "tags") // list<string>
	input := []interface{}{"a", "b", "c"}

	if err := c.writeValue(ctx, prot, ft, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, ft)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 items, got %d", len(list))
	}
	for i, expected := range []string{"a", "b", "c"} {
		if list[i] != expected {
			t.Errorf("list[%d] = %v, want %v", i, list[i], expected)
		}
	}
}

func TestCodecSet(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ft := fieldType(schema, "User", "roles") // set<string>
	input := []interface{}{"admin", "editor"}

	if err := c.writeValue(ctx, prot, ft, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, ft)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
}

func TestCodecMap(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ft := fieldType(schema, "User", "scores") // map<string, i32>
	input := map[string]interface{}{
		"foo": float64(1),
		"bar": float64(2),
	}

	if err := c.writeValue(ctx, prot, ft, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, ft)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if len(m) != 2 {
		t.Errorf("expected 2 items, got %d", len(m))
	}
	if m["foo"] != float64(1) {
		t.Errorf("m[foo] = %v, want 1", m["foo"])
	}
}

func TestCodecStruct(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ft := fieldType(schema, "User", "address") // Address struct
	input := map[string]interface{}{
		"street": "123 Main St",
		"city":   "Springfield",
		"zip":    float64(12345),
	}

	if err := c.writeValue(ctx, prot, ft, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, ft)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["street"] != "123 Main St" {
		t.Errorf("street = %v, want '123 Main St'", m["street"])
	}
	if m["city"] != "Springfield" {
		t.Errorf("city = %v, want 'Springfield'", m["city"])
	}
	if m["zip"] != float64(12345) {
		t.Errorf("zip = %v, want 12345", m["zip"])
	}
}

func TestCodecNestedStruct(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	// Write a full User struct.
	// Need a User type reference from the schema.
	ms := schema.methods["getUser"]
	userType := ms.returnType

	input := map[string]interface{}{
		"name": "John",
		"age":  float64(30),
		"address": map[string]interface{}{
			"street": "456 Oak Ave",
			"city":   "Shelbyville",
			"zip":    float64(67890),
		},
		"tags":   []interface{}{"admin", "user"},
		"active": true,
		"rating": float64(4.5),
	}

	if err := c.writeValue(ctx, prot, userType, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, userType)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["name"] != "John" {
		t.Errorf("name = %v, want 'John'", m["name"])
	}
	addr, ok := m["address"].(map[string]interface{})
	if !ok {
		t.Fatalf("address should be a map, got %T", m["address"])
	}
	if addr["city"] != "Shelbyville" {
		t.Errorf("address.city = %v, want 'Shelbyville'", addr["city"])
	}
}

func TestCodecEnum(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()

	ft := fieldType(schema, "User", "status") // Status enum

	// Test enum by numeric value.
	t.Run("numeric", func(t *testing.T) {
		prot, _ := newMemoryProtocol()

		if err := c.writeValue(ctx, prot, ft, float64(1)); err != nil {
			t.Fatalf("writeValue failed: %v", err)
		}

		result, err := c.readValue(ctx, prot, ft)
		if err != nil {
			t.Fatalf("readValue failed: %v", err)
		}
		if result != "ACTIVE" {
			t.Errorf("expected ACTIVE, got %v", result)
		}
	})

	// Test enum by string name.
	t.Run("string_name", func(t *testing.T) {
		prot, _ := newMemoryProtocol()

		if err := c.writeValue(ctx, prot, ft, "INACTIVE"); err != nil {
			t.Fatalf("writeValue failed: %v", err)
		}

		result, err := c.readValue(ctx, prot, ft)
		if err != nil {
			t.Fatalf("readValue failed: %v", err)
		}
		if result != "INACTIVE" {
			t.Errorf("expected INACTIVE, got %v", result)
		}
	})
}

func TestCodecOptionalMissing(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	userType := schema.methods["getUser"].returnType
	input := map[string]interface{}{
		"name": "Jane",
	}

	if err := c.writeValue(ctx, prot, userType, input); err != nil {
		t.Fatalf("writeValue failed: %v", err)
	}

	result, err := c.readValue(ctx, prot, userType)
	if err != nil {
		t.Fatalf("readValue failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["name"] != "Jane" {
		t.Errorf("name = %v, want 'Jane'", m["name"])
	}
	if _, ok := m["age"]; ok {
		t.Error("age should be absent when not set")
	}
}

func TestCodecTypeMismatch(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ft := fieldType(schema, "User", "active") // bool
	err := c.writeValue(ctx, prot, ft, "not a bool")
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
}

func TestCodecArgsRoundtrip(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	prot, _ := newMemoryProtocol()

	ms := schema.methods["getUser"]
	jsonData := map[string]interface{}{
		"id": "user-456",
	}

	if err := c.jsonToThriftArgs(ctx, prot, ms.args, jsonData); err != nil {
		t.Fatalf("jsonToThriftArgs failed: %v", err)
	}

	// Read it back as a struct.
	if _, err := prot.ReadStructBegin(ctx); err != nil {
		t.Fatalf("ReadStructBegin failed: %v", err)
	}

	_, _, fieldID, err := prot.ReadFieldBegin(ctx)
	if err != nil {
		t.Fatalf("ReadFieldBegin failed: %v", err)
	}
	if fieldID != 1 {
		t.Errorf("expected field ID 1, got %d", fieldID)
	}

	val, err := prot.ReadString(ctx)
	if err != nil {
		t.Fatalf("ReadString failed: %v", err)
	}
	if val != "user-456" {
		t.Errorf("expected 'user-456', got %q", val)
	}
}

func TestCodecResultRoundtrip(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	ms := schema.methods["getUser"]

	// Simulate a Thrift reply: write a success result (field 0 = User struct).
	prot, _ := newMemoryProtocol()

	prot.WriteStructBegin(ctx, "result")
	prot.WriteFieldBegin(ctx, "success", athrift.STRUCT, 0)

	// Write a simple User struct.
	prot.WriteStructBegin(ctx, "User")
	prot.WriteFieldBegin(ctx, "name", athrift.STRING, 1)
	prot.WriteString(ctx, "TestUser")
	prot.WriteFieldEnd(ctx)
	prot.WriteFieldStop(ctx)
	prot.WriteStructEnd(ctx)

	prot.WriteFieldEnd(ctx)
	prot.WriteFieldStop(ctx)
	prot.WriteStructEnd(ctx)

	resultJSON, err := c.readResult(ctx, prot, ms)
	if err != nil {
		t.Fatalf("readResult failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if result["name"] != "TestUser" {
		t.Errorf("expected name=TestUser, got %v", result["name"])
	}
}

func TestCodecVoidResult(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	ms := schema.methods["createUser"]

	prot, _ := newMemoryProtocol()
	prot.WriteStructBegin(ctx, "result")
	prot.WriteFieldStop(ctx)
	prot.WriteStructEnd(ctx)

	resultJSON, err := c.readResult(ctx, prot, ms)
	if err != nil {
		t.Fatalf("readResult failed: %v", err)
	}

	if string(resultJSON) != "{}" {
		t.Errorf("expected '{}', got %q", string(resultJSON))
	}
}

func TestCodecExceptionResult(t *testing.T) {
	c, schema := newTestCodec(t)
	ctx := context.Background()
	ms := schema.methods["updateUser"]

	// Write a result with exception in field 1 (not_found).
	prot, _ := newMemoryProtocol()
	prot.WriteStructBegin(ctx, "result")
	prot.WriteFieldBegin(ctx, "not_found", athrift.STRUCT, 1)

	// Write NotFoundError struct.
	prot.WriteStructBegin(ctx, "NotFoundError")
	prot.WriteFieldBegin(ctx, "message", athrift.STRING, 1)
	prot.WriteString(ctx, "user not found")
	prot.WriteFieldEnd(ctx)
	prot.WriteFieldStop(ctx)
	prot.WriteStructEnd(ctx)

	prot.WriteFieldEnd(ctx)
	prot.WriteFieldStop(ctx)
	prot.WriteStructEnd(ctx)

	_, err := c.readResult(ctx, prot, ms)
	if err == nil {
		t.Fatal("expected error for exception result")
	}
	if !contains(err.Error(), "thrift exception") {
		t.Errorf("expected 'thrift exception' in error, got: %v", err)
	}
}

// -- Test helpers --

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("got %v (%T), want %v (%T)", got, got, want, want)
	}
}

func assertFloat(t *testing.T, got interface{}, want float64) {
	t.Helper()
	f, ok := got.(float64)
	if !ok {
		t.Errorf("expected float64, got %T", got)
		return
	}
	if f != want {
		t.Errorf("got %v, want %v", f, want)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
