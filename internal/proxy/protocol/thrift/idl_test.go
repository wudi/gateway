package thrift

import (
	"testing"

	"github.com/cloudwego/thriftgo/parser"
)

const testIDL = `
namespace go test

enum Status {
  ACTIVE = 1
  INACTIVE = 2
}

struct Address {
  1: string street
  2: string city
  3: i32 zip
}

struct User {
  1: required string name
  2: optional i32 age
  3: Address address
  4: list<string> tags
  5: map<string, i32> scores
  6: Status status
  7: binary avatar
  8: bool active
  9: double rating
  10: i64 created_at
  11: set<string> roles
}

exception NotFoundError {
  1: string message
}

exception ValidationError {
  1: string field
  2: string reason
}

typedef string UserID

service UserService {
  User getUser(1: UserID id)
  void createUser(1: User user)
  oneway void notifyUser(1: string id, 2: string message)
  User updateUser(1: string id, 2: User user) throws (1: NotFoundError not_found, 2: ValidationError validation)
  list<User> listUsers(1: i32 limit, 2: i32 offset)
}
`

func TestParseServiceSchema(t *testing.T) {
	cache := newSchemaCache()
	schema, err := cache.getServiceSchemaFromContent("test.thrift", testIDL, "UserService")
	if err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	// Verify service name.
	if schema.service.Name != "UserService" {
		t.Errorf("expected service name UserService, got %s", schema.service.Name)
	}

	// Verify methods.
	expectedMethods := []string{"getUser", "createUser", "notifyUser", "updateUser", "listUsers"}
	for _, name := range expectedMethods {
		if _, ok := schema.methods[name]; !ok {
			t.Errorf("missing method: %s", name)
		}
	}

	// Verify oneway detection.
	if ms, ok := schema.methods["notifyUser"]; ok {
		if !ms.oneway {
			t.Error("notifyUser should be oneway")
		}
	}

	// Verify void return type.
	if ms, ok := schema.methods["createUser"]; ok {
		if !ms.void {
			t.Error("createUser should be void")
		}
	}

	// Verify throws.
	if ms, ok := schema.methods["updateUser"]; ok {
		if len(ms.throws) != 2 {
			t.Errorf("updateUser should have 2 exceptions, got %d", len(ms.throws))
		}
	}

	// Verify struct collection.
	for _, name := range []string{"User", "Address", "NotFoundError", "ValidationError"} {
		if _, ok := schema.structs[name]; !ok {
			t.Errorf("missing struct: %s", name)
		}
	}

	// Verify enum collection.
	if _, ok := schema.enums["Status"]; !ok {
		t.Error("missing enum: Status")
	}

	// Verify typedef.
	if _, ok := schema.typedefs["UserID"]; !ok {
		t.Error("missing typedef: UserID")
	}

	// Verify field details on User struct.
	userStruct := schema.structs["User"]
	if len(userStruct.Fields) != 11 {
		t.Errorf("User should have 11 fields, got %d", len(userStruct.Fields))
	}
}

func TestParseServiceSchema_MissingService(t *testing.T) {
	cache := newSchemaCache()
	_, err := cache.getServiceSchemaFromContent("test.thrift", testIDL, "NonExistentService")
	if err == nil {
		t.Fatal("expected error for missing service")
	}
}

func TestParseServiceSchema_InvalidIDL(t *testing.T) {
	cache := newSchemaCache()
	_, err := cache.getServiceSchemaFromContent("test.thrift", "invalid thrift content {{{", "MyService")
	if err == nil {
		t.Fatal("expected error for invalid IDL")
	}
}

func TestFieldCategories(t *testing.T) {
	// Verify that semantic resolution fills in correct categories.
	cache := newSchemaCache()
	schema, err := cache.getServiceSchemaFromContent("test.thrift", testIDL, "UserService")
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	userStruct := schema.structs["User"]
	if userStruct == nil {
		t.Fatal("User struct not found")
	}

	expectedCategories := map[string]parser.Category{
		"name":       parser.Category_String,
		"age":        parser.Category_I32,
		"active":     parser.Category_Bool,
		"rating":     parser.Category_Double,
		"created_at": parser.Category_I64,
		"tags":       parser.Category_List,
		"scores":     parser.Category_Map,
		"avatar":     parser.Category_Binary,
		"roles":      parser.Category_Set,
	}

	for _, f := range userStruct.Fields {
		if expected, ok := expectedCategories[f.Name]; ok {
			if f.Type.Category != expected {
				t.Errorf("field %s: expected category %v, got %v", f.Name, expected, f.Type.Category)
			}
		}
	}
}

func TestMethodArgs(t *testing.T) {
	cache := newSchemaCache()
	schema, err := cache.getServiceSchemaFromContent("test.thrift", testIDL, "UserService")
	if err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	// getUser should have 1 arg.
	if ms, ok := schema.methods["getUser"]; ok {
		if len(ms.args) != 1 {
			t.Errorf("getUser should have 1 arg, got %d", len(ms.args))
		}
		if ms.args[0].Name != "id" {
			t.Errorf("getUser arg should be 'id', got %q", ms.args[0].Name)
		}
	}

	// notifyUser should have 2 args.
	if ms, ok := schema.methods["notifyUser"]; ok {
		if len(ms.args) != 2 {
			t.Errorf("notifyUser should have 2 args, got %d", len(ms.args))
		}
	}

	// listUsers should have 2 args (limit, offset).
	if ms, ok := schema.methods["listUsers"]; ok {
		if len(ms.args) != 2 {
			t.Errorf("listUsers should have 2 args, got %d", len(ms.args))
		}
	}
}
