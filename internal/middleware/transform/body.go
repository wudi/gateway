package transform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/template"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
)

// compiledFlatmapOp is a pre-validated flatmap operation.
type compiledFlatmapOp struct {
	opType string
	args   []string
}

// CompiledBodyTransform is a pre-compiled body transformation engine.
// Created once per route at init, safe for concurrent use.
type CompiledBodyTransform struct {
	setFields    map[string]*variables.CompiledTemplate
	addFields    map[string]*variables.CompiledTemplate
	removeFields []string
	renameFields map[string]string
	allowFields  []string
	denyFields   []string
	tmpl         *template.Template
	resolver     *variables.Resolver
	target       string              // gjson path to extract as root
	flatmapOps   []compiledFlatmapOp // pre-validated flatmap operations
}

// NewCompiledBodyTransform creates a new compiled body transform from config.
func NewCompiledBodyTransform(cfg config.BodyTransformConfig) (*CompiledBodyTransform, error) {
	resolver := variables.NewResolver()
	ct := &CompiledBodyTransform{
		removeFields: cfg.RemoveFields,
		renameFields: cfg.RenameFields,
		allowFields:  cfg.AllowFields,
		denyFields:   cfg.DenyFields,
		resolver:     resolver,
	}

	// Pre-compile set_fields value templates
	if len(cfg.SetFields) > 0 {
		ct.setFields = make(map[string]*variables.CompiledTemplate, len(cfg.SetFields))
		for path, val := range cfg.SetFields {
			ct.setFields[path] = resolver.PrecompileTemplate(val)
		}
	}

	// Pre-compile add_fields value templates
	if len(cfg.AddFields) > 0 {
		ct.addFields = make(map[string]*variables.CompiledTemplate, len(cfg.AddFields))
		for key, val := range cfg.AddFields {
			ct.addFields[key] = resolver.PrecompileTemplate(val)
		}
	}

	// Store target path
	ct.target = cfg.Target

	// Pre-validate flatmap operations
	if len(cfg.Flatmap) > 0 {
		ct.flatmapOps = make([]compiledFlatmapOp, len(cfg.Flatmap))
		for i, op := range cfg.Flatmap {
			ct.flatmapOps[i] = compiledFlatmapOp{
				opType: op.Type,
				args:   op.Args,
			}
		}
	}

	// Parse Go template
	if cfg.Template != "" {
		funcMap := template.FuncMap{
			"json": func(v interface{}) (string, error) {
				b, err := json.Marshal(v)
				return string(b), err
			},
		}
		tmpl, err := template.New("body").Funcs(funcMap).Parse(cfg.Template)
		if err != nil {
			return nil, fmt.Errorf("invalid body template: %w", err)
		}
		ct.tmpl = tmpl
	}

	return ct, nil
}

// Transform applies all body transformations in order.
// Processing order: target → allow/deny → set_fields → add_fields → remove_fields → rename_fields → flatmap → template
func (ct *CompiledBodyTransform) Transform(body []byte, varCtx *variables.Context) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}

	// 0. target — extract nested path as root
	if ct.target != "" {
		body = ct.applyTarget(body)
	}

	// 1. Allow/deny filter
	if len(ct.allowFields) > 0 {
		body = ct.applyAllowFilter(body)
	} else if len(ct.denyFields) > 0 {
		body = ct.applyDenyFilter(body)
	}

	// 2. set_fields — sjson.SetBytes at each path
	for path, tmpl := range ct.setFields {
		val := tmpl.Resolve(varCtx)
		typed := inferType(val)
		var err error
		body, err = sjson.SetBytes(body, path, typed)
		if err != nil {
			continue
		}
	}

	// 3. add_fields — sjson.SetBytes at top-level keys (backward compat)
	for key, tmpl := range ct.addFields {
		val := tmpl.Resolve(varCtx)
		typed := inferType(val)
		var err error
		body, err = sjson.SetBytes(body, key, typed)
		if err != nil {
			continue
		}
	}

	// 4. remove_fields — sjson.DeleteBytes (supports dot paths)
	for _, path := range ct.removeFields {
		var err error
		body, err = sjson.DeleteBytes(body, path)
		if err != nil {
			continue
		}
	}

	// 5. rename_fields — gjson.GetBytes + sjson.SetRawBytes + sjson.DeleteBytes
	for oldKey, newKey := range ct.renameFields {
		result := gjson.GetBytes(body, oldKey)
		if !result.Exists() {
			continue
		}
		var err error
		body, err = sjson.SetRawBytes(body, newKey, []byte(result.Raw))
		if err != nil {
			continue
		}
		body, _ = sjson.DeleteBytes(body, oldKey)
	}

	// 6. flatmap — array manipulation operations
	if len(ct.flatmapOps) > 0 {
		body = ct.applyFlatmap(body)
	}

	// 7. template — terminal operation
	if ct.tmpl != nil {
		body = ct.applyTemplate(body, varCtx)
	}

	return body
}

// TransformRequest reads the request body, transforms it, and replaces r.Body.
func (ct *CompiledBodyTransform) TransformRequest(r *http.Request, varCtx *variables.Context) {
	if r.Body == nil {
		return
	}

	if !isJSON(r.Header.Get("Content-Type")) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	r.Body.Close()

	transformed := ct.Transform(body, varCtx)

	r.Body = io.NopCloser(bytes.NewReader(transformed))
	r.ContentLength = int64(len(transformed))
}

// ResponseBodyTransformMiddleware creates a middleware that buffers the response,
// transforms the JSON body, and replays it to the client.
func ResponseBodyTransformMiddleware(ct *CompiledBodyTransform) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()
			if isJSON(bw.header.Get("Content-Type")) && len(body) > 0 {
				varCtx := variables.GetFromRequest(r)
				body = ct.Transform(body, varCtx)
			}

			// Copy captured headers to real writer
			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(bw.statusCode)
			w.Write(body)
		})
	}
}

// bodyBufferWriter captures the response for transformation.
type bodyBufferWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func (bw *bodyBufferWriter) Header() http.Header {
	return bw.header
}

func (bw *bodyBufferWriter) WriteHeader(code int) {
	bw.statusCode = code
}

func (bw *bodyBufferWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
}

// applyTarget extracts a nested path as the root response.
func (ct *CompiledBodyTransform) applyTarget(body []byte) []byte {
	result := gjson.GetBytes(body, ct.target)
	if !result.Exists() {
		return body
	}
	raw := []byte(result.Raw)
	if !gjson.ValidBytes(raw) {
		return body
	}
	return raw
}

// applyFlatmap applies all flatmap operations in order.
func (ct *CompiledBodyTransform) applyFlatmap(body []byte) []byte {
	for _, op := range ct.flatmapOps {
		switch op.opType {
		case "move":
			if len(op.args) >= 2 {
				body = ct.flatmapMove(body, op.args[0], op.args[1])
			}
		case "del":
			if len(op.args) >= 1 {
				body, _ = sjson.DeleteBytes(body, op.args[0])
			}
		case "extract":
			if len(op.args) >= 2 {
				body = ct.flatmapExtract(body, op.args[0], op.args[1])
			}
		case "flatten":
			if len(op.args) >= 1 {
				body = ct.flatmapFlatten(body, op.args[0])
			}
		case "append":
			if len(op.args) >= 2 {
				body = ct.flatmapAppend(body, op.args[0], op.args[1:])
			}
		}
	}
	return body
}

// flatmapMove moves a value from source path to dest path.
func (ct *CompiledBodyTransform) flatmapMove(body []byte, src, dst string) []byte {
	result := gjson.GetBytes(body, src)
	if !result.Exists() {
		return body
	}
	var err error
	body, err = sjson.SetRawBytes(body, dst, []byte(result.Raw))
	if err != nil {
		return body
	}
	body, _ = sjson.DeleteBytes(body, src)
	return body
}

// flatmapExtract extracts a field from each object in an array.
// e.g., [{"id":1,"name":"a"},{"id":2,"name":"b"}] extract "name" → ["a","b"]
func (ct *CompiledBodyTransform) flatmapExtract(body []byte, arrayPath, fieldName string) []byte {
	// Use gjson's multipaths: arrayPath.#.fieldName gives us the extracted array
	extracted := gjson.GetBytes(body, arrayPath+".#."+fieldName)
	if !extracted.Exists() {
		return body
	}
	var err error
	body, err = sjson.SetRawBytes(body, arrayPath, []byte(extracted.Raw))
	if err != nil {
		return body
	}
	return body
}

// flatmapFlatten flattens nested arrays at the given path.
// e.g., [[1,2],[3,4]] → [1,2,3,4]
func (ct *CompiledBodyTransform) flatmapFlatten(body []byte, path string) []byte {
	result := gjson.GetBytes(body, path)
	if !result.Exists() || !result.IsArray() {
		return body
	}

	var flat []json.RawMessage
	result.ForEach(func(_, value gjson.Result) bool {
		if value.IsArray() {
			value.ForEach(func(_, inner gjson.Result) bool {
				flat = append(flat, json.RawMessage(inner.Raw))
				return true
			})
		} else {
			flat = append(flat, json.RawMessage(value.Raw))
		}
		return true
	})

	flatJSON, err := json.Marshal(flat)
	if err != nil {
		return body
	}
	body, err = sjson.SetRawBytes(body, path, flatJSON)
	if err != nil {
		return body
	}
	return body
}

// flatmapAppend concatenates source arrays into the dest array.
func (ct *CompiledBodyTransform) flatmapAppend(body []byte, dest string, sources []string) []byte {
	// Start with existing dest array or empty
	var combined []json.RawMessage
	destResult := gjson.GetBytes(body, dest)
	if destResult.Exists() && destResult.IsArray() {
		destResult.ForEach(func(_, value gjson.Result) bool {
			combined = append(combined, json.RawMessage(value.Raw))
			return true
		})
	}

	for _, src := range sources {
		srcResult := gjson.GetBytes(body, src)
		if !srcResult.Exists() || !srcResult.IsArray() {
			continue
		}
		srcResult.ForEach(func(_, value gjson.Result) bool {
			combined = append(combined, json.RawMessage(value.Raw))
			return true
		})
	}

	combinedJSON, err := json.Marshal(combined)
	if err != nil {
		return body
	}
	body, err = sjson.SetRawBytes(body, dest, combinedJSON)
	if err != nil {
		return body
	}
	return body
}

// applyAllowFilter builds a new JSON object containing only the allowed fields.
func (ct *CompiledBodyTransform) applyAllowFilter(body []byte) []byte {
	result := []byte("{}")
	for _, path := range ct.allowFields {
		val := gjson.GetBytes(body, path)
		if !val.Exists() {
			continue
		}
		var err error
		result, err = sjson.SetRawBytes(result, path, []byte(val.Raw))
		if err != nil {
			continue
		}
	}
	return result
}

// applyDenyFilter removes denied fields from the JSON body.
func (ct *CompiledBodyTransform) applyDenyFilter(body []byte) []byte {
	for _, path := range ct.denyFields {
		var err error
		body, err = sjson.DeleteBytes(body, path)
		if err != nil {
			continue
		}
	}
	return body
}

// applyTemplate executes the Go template with body and vars data.
func (ct *CompiledBodyTransform) applyTemplate(body []byte, varCtx *variables.Context) []byte {
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	data := map[string]interface{}{
		"body": parsed,
		"vars": buildVarsMap(varCtx),
	}

	var buf bytes.Buffer
	if err := ct.tmpl.Execute(&buf, data); err != nil {
		return body
	}

	out := buf.Bytes()
	if !gjson.ValidBytes(out) {
		return body
	}
	return out
}

// buildVarsMap exposes common gateway variables for template use.
func buildVarsMap(varCtx *variables.Context) map[string]string {
	if varCtx == nil {
		return map[string]string{}
	}
	m := map[string]string{
		"request_id": varCtx.RequestID,
		"route_id":   varCtx.RouteID,
	}
	if varCtx.Request != nil {
		m["request_method"] = varCtx.Request.Method
		m["request_path"] = varCtx.Request.URL.Path
		m["host"] = varCtx.Request.Host
	}
	resolver := variables.NewResolver()
	for _, name := range []string{"time_unix", "time_iso8601", "remote_addr"} {
		val, ok := resolver.Get(name, varCtx)
		if ok {
			m[name] = val
		}
	}
	return m
}

// inferType attempts to parse a string value as a Go native type for JSON encoding.
func inferType(s string) interface{} {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if s == "null" {
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// isJSON checks if the content type indicates JSON.
func isJSON(contentType string) bool {
	return strings.HasPrefix(contentType, "application/json")
}
