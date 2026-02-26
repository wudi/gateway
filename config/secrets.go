package config

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

// SecretProvider resolves secret references for a given scheme.
type SecretProvider interface {
	Scheme() string
	Resolve(ctx context.Context, reference string) (string, error)
}

// SecretRegistry manages named SecretProviders.
type SecretRegistry struct {
	providers map[string]SecretProvider
}

// NewSecretRegistry creates an empty registry.
func NewSecretRegistry() *SecretRegistry {
	return &SecretRegistry{providers: make(map[string]SecretProvider)}
}

// Register adds a provider to the registry. It overwrites any existing
// provider for the same scheme.
func (r *SecretRegistry) Register(p SecretProvider) {
	r.providers[p.Scheme()] = p
}

// Clone returns a shallow copy so per-parse additions don't mutate the base.
func (r *SecretRegistry) Clone() *SecretRegistry {
	c := &SecretRegistry{providers: make(map[string]SecretProvider, len(r.providers))}
	for k, v := range r.providers {
		c.providers[k] = v
	}
	return c
}

// Resolve looks up the provider for scheme and delegates resolution.
func (r *SecretRegistry) Resolve(ctx context.Context, scheme, reference string) (string, error) {
	p, ok := r.providers[scheme]
	if !ok {
		return "", fmt.Errorf("unknown secret provider scheme %q", scheme)
	}
	return p.Resolve(ctx, reference)
}

// Close calls Close on any provider that implements io.Closer.
func (r *SecretRegistry) Close() error {
	var errs []string
	for _, p := range r.providers {
		if c, ok := p.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("closing secret providers: %s", strings.Join(errs, "; "))
	}
	return nil
}

// secretRefPattern matches a full-string secret reference: ${scheme:reference}
// scheme must start with lowercase letter followed by lowercase letters/digits.
var secretRefPattern = regexp.MustCompile(`^\$\{([a-z][a-z0-9]*):(.+)\}$`)

// resolveSecretRefs walks a config struct resolving ${scheme:ref} strings in place.
func resolveSecretRefs(cfg any, registry *SecretRegistry, ctx context.Context) error {
	var resolveErr error
	walkStructStrings(reflect.ValueOf(cfg), "", func(field reflect.Value, path string, _ reflect.StructTag) {
		if resolveErr != nil {
			return
		}
		val := field.String()
		if val == "" {
			return
		}
		m := secretRefPattern.FindStringSubmatch(val)
		if m == nil {
			return
		}
		scheme, ref := m[1], m[2]
		resolved, err := registry.Resolve(ctx, scheme, ref)
		if err != nil {
			resolveErr = fmt.Errorf("secret resolution failed for %s (${%s:%s}): %w", path, scheme, ref, err)
			return
		}
		field.SetString(resolved)
	})
	return resolveErr
}

// walkStructStrings walks a reflect.Value recursively, calling fn for every
// settable string field it encounters. path is a dotted field path for error
// messages. This helper is shared by resolveSecretRefs and redactFields.
func walkStructStrings(v reflect.Value, path string, fn func(field reflect.Value, path string, tag reflect.StructTag)) {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		walkStructStrings(v.Elem(), path, fn)

	case reflect.Struct:
		// Skip types that should not be traversed.
		t := v.Type()
		if t == reflect.TypeOf(yaml.RawMessage{}) {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := v.Field(i)
			sf := t.Field(i)
			if !f.CanSet() {
				continue
			}
			fieldPath := sf.Name
			if path != "" {
				fieldPath = path + "." + sf.Name
			}

			switch f.Kind() {
			case reflect.String:
				fn(f, fieldPath, sf.Tag)
			case reflect.Struct:
				walkStructStrings(f, fieldPath, fn)
			case reflect.Ptr:
				walkStructStrings(f, fieldPath, fn)
			case reflect.Slice:
				walkSliceStrings(f, fieldPath, fn)
			case reflect.Map:
				walkMapStrings(f, fieldPath, fn)
			}
		}
	}
}

func walkSliceStrings(v reflect.Value, path string, fn func(field reflect.Value, path string, tag reflect.StructTag)) {
	if v.IsNil() {
		return
	}
	elemType := v.Type().Elem()
	// Only recurse into slices of structs or pointers-to-structs.
	switch elemType.Kind() {
	case reflect.Struct:
		if elemType == reflect.TypeOf(yaml.RawMessage{}) {
			return
		}
		// Skip []byte ([]uint8)
		if elemType == reflect.TypeOf(byte(0)) {
			return
		}
		for i := 0; i < v.Len(); i++ {
			walkStructStrings(v.Index(i).Addr(), fmt.Sprintf("%s[%d]", path, i), fn)
		}
	case reflect.Ptr:
		for i := 0; i < v.Len(); i++ {
			walkStructStrings(v.Index(i), fmt.Sprintf("%s[%d]", path, i), fn)
		}
	}
}

func walkMapStrings(v reflect.Value, path string, fn func(field reflect.Value, path string, tag reflect.StructTag)) {
	if v.IsNil() {
		return
	}
	elemType := v.Type().Elem()
	// Only recurse into maps with struct-typed values (not map[string]string, yaml.RawMessage, etc.)
	switch elemType.Kind() {
	case reflect.Struct:
		if elemType == reflect.TypeOf(yaml.RawMessage{}) {
			return
		}
		for _, key := range v.MapKeys() {
			// Map values are not addressable, so copy → walk → set back.
			elem := v.MapIndex(key)
			cp := reflect.New(elemType).Elem()
			cp.Set(elem)
			walkStructStrings(cp.Addr(), fmt.Sprintf("%s[%s]", path, key.String()), fn)
			v.SetMapIndex(key, cp)
		}
	case reflect.Ptr:
		for _, key := range v.MapKeys() {
			walkStructStrings(v.MapIndex(key), fmt.Sprintf("%s[%s]", path, key.String()), fn)
		}
	}
}
