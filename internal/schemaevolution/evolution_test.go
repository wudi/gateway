package schemaevolution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/runway/config"
	"go.uber.org/zap"
)

func loadSpec(t *testing.T, yaml string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}
	return doc
}

const baseSpec = `
openapi: "3.0.0"
info:
  title: Test API
  version: "1.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
        - name: status
          in: query
          schema:
            type: string
            enum: ["active", "inactive", "pending"]
      responses:
        "200":
          description: Users list
          content:
            application/json:
              schema:
                type: object
                required: ["users", "total"]
                properties:
                  users:
                    type: array
                    items:
                      type: object
                  total:
                    type: integer
    post:
      operationId: createUser
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: ["name"]
              properties:
                name:
                  type: string
                email:
                  type: string
      responses:
        "201":
          description: Created
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A user
`

func TestNoBreakingChanges(t *testing.T) {
	old := loadSpec(t, baseSpec)
	new := loadSpec(t, baseSpec)

	changes := detectBreakingChanges(old, new)
	if len(changes) != 0 {
		t.Errorf("expected no breaking changes, got %d: %+v", len(changes), changes)
	}
}

func TestEndpointRemoved(t *testing.T) {
	old := loadSpec(t, baseSpec)
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: Users list
`
	new := loadSpec(t, newSpec)

	changes := detectBreakingChanges(old, new)

	found := false
	for _, c := range changes {
		if c.Type == "endpoint_removed" && c.Path == "/users/{id}" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected endpoint_removed for /users/{id}, got: %+v", changes)
	}
}

func TestMethodRemoved(t *testing.T) {
	old := loadSpec(t, baseSpec)
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: Users list
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A user
`
	new := loadSpec(t, newSpec)

	changes := detectBreakingChanges(old, new)

	found := false
	for _, c := range changes {
		if c.Type == "method_removed" && c.Path == "/users" && c.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected method_removed for POST /users, got: %+v", changes)
	}
}

func TestRequiredParamAdded(t *testing.T) {
	old := loadSpec(t, baseSpec)
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
        - name: status
          in: query
          schema:
            type: string
        - name: tenant_id
          in: header
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Users list
          content:
            application/json:
              schema:
                type: object
                required: ["users", "total"]
                properties:
                  users:
                    type: array
                    items:
                      type: object
                  total:
                    type: integer
    post:
      operationId: createUser
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: ["name"]
              properties:
                name:
                  type: string
                email:
                  type: string
      responses:
        "201":
          description: Created
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A user
`
	new := loadSpec(t, newSpec)

	changes := detectBreakingChanges(old, new)

	found := false
	for _, c := range changes {
		if c.Type == "required_param_added" && c.Path == "/users" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected required_param_added, got: %+v", changes)
	}
}

func TestEnumValueRemoved(t *testing.T) {
	old := loadSpec(t, baseSpec)
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
        - name: status
          in: query
          schema:
            type: string
            enum: ["active", "inactive"]
      responses:
        "200":
          description: Users list
          content:
            application/json:
              schema:
                type: object
                required: ["users", "total"]
                properties:
                  users:
                    type: array
                    items:
                      type: object
                  total:
                    type: integer
    post:
      operationId: createUser
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: ["name"]
              properties:
                name:
                  type: string
                email:
                  type: string
      responses:
        "201":
          description: Created
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A user
`
	new := loadSpec(t, newSpec)

	changes := detectBreakingChanges(old, new)

	found := false
	for _, c := range changes {
		if c.Type == "enum_value_removed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected enum_value_removed, got: %+v", changes)
	}
}

func TestRequiredResponseFieldRemoved(t *testing.T) {
	old := loadSpec(t, baseSpec)
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: Users list
          content:
            application/json:
              schema:
                type: object
                required: ["users"]
                properties:
                  users:
                    type: array
                    items:
                      type: object
    post:
      operationId: createUser
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: ["name"]
              properties:
                name:
                  type: string
                email:
                  type: string
      responses:
        "201":
          description: Created
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A user
`
	new := loadSpec(t, newSpec)

	changes := detectBreakingChanges(old, new)

	found := false
	for _, c := range changes {
		if c.Type == "required_response_field_removed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected required_response_field_removed, got: %+v", changes)
	}
}

func TestCheckerWarnMode(t *testing.T) {
	dir := t.TempDir()
	checker, err := NewChecker(config.SchemaEvolutionConfig{
		Enabled:     true,
		Mode:        "warn",
		StoreDir:    dir,
		MaxVersions: 5,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	old := loadSpec(t, baseSpec)
	_, err = checker.CheckAndStore("test-api", old)
	if err != nil {
		t.Fatalf("first store should not error: %v", err)
	}

	// Remove an endpoint
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: Users list
`
	new := loadSpec(t, newSpec)
	report, err := checker.CheckAndStore("test-api", new)
	if err != nil {
		t.Errorf("warn mode should not return error, got: %v", err)
	}
	if report == nil {
		t.Fatal("expected report")
	}
	if report.Compatible {
		t.Error("expected incompatible report")
	}
	if len(report.BreakingChanges) == 0 {
		t.Error("expected breaking changes")
	}
}

func TestCheckerBlockMode(t *testing.T) {
	dir := t.TempDir()
	checker, err := NewChecker(config.SchemaEvolutionConfig{
		Enabled:     true,
		Mode:        "block",
		StoreDir:    dir,
		MaxVersions: 5,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	old := loadSpec(t, baseSpec)
	_, err = checker.CheckAndStore("test-api", old)
	if err != nil {
		t.Fatalf("first store should not error: %v", err)
	}

	// Remove an endpoint
	newSpec := `
openapi: "3.0.0"
info:
  title: Test API
  version: "2.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: Users list
`
	new := loadSpec(t, newSpec)
	_, err = checker.CheckAndStore("test-api", new)
	if err == nil {
		t.Error("block mode should return error on breaking changes")
	}
}

func TestSpecStorePruning(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpecStore(dir, 3)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	spec := loadSpec(t, baseSpec)
	for i := 0; i < 5; i++ {
		if err := store.Store("test", spec); err != nil {
			t.Fatalf("store iteration %d: %v", i, err)
		}
	}

	entries, _ := filepath.Glob(filepath.Join(dir, "test_*.json"))
	if len(entries) > 3 {
		t.Errorf("expected max 3 entries, got %d", len(entries))
	}
}

func TestCheckerGetReports(t *testing.T) {
	dir := t.TempDir()
	checker, err := NewChecker(config.SchemaEvolutionConfig{
		Enabled:  true,
		Mode:     "warn",
		StoreDir: dir,
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	spec := loadSpec(t, baseSpec)
	checker.CheckAndStore("api-1", spec)
	checker.CheckAndStore("api-1", spec)

	if r := checker.GetReport("api-1"); r == nil {
		t.Error("expected report for api-1")
	}
	if r := checker.GetReport("unknown"); r != nil {
		t.Error("expected nil for unknown spec")
	}

	all := checker.GetAllReports()
	if len(all) != 1 {
		t.Errorf("expected 1 report, got %d", len(all))
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"specs/users.yaml", "specs_users_yaml"},
		{"my-api", "my-api"},
		{"a/b.c:d", "a_b_c_d"},
	}
	for _, tt := range tests {
		got := sanitizeID(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSpecStoreEmpty(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSpecStore(dir, 10)

	doc, version, err := store.GetPrevious("nonexistent")
	if doc != nil || version != "" || err != nil {
		t.Errorf("expected all nil/empty for nonexistent spec, got doc=%v version=%q err=%v", doc, version, err)
	}
}

func TestSpecStoreCreateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	_, err := NewSpecStore(dir, 10)
	if err != nil {
		t.Fatalf("should create nested dir: %v", err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected directory to exist")
	}
}
