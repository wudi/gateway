package tmplutil

import (
	"encoding/json"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// FuncMap returns the shared template function map used by all runway
// template compilation sites. It includes all Sprig functions plus
// runway-specific helpers (json, first, pick).
func FuncMap() template.FuncMap {
	fm := sprig.TxtFuncMap()

	// Gateway-specific helpers
	fm["json"] = func(v interface{}) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	}
	fm["first"] = func(vals []string) string {
		if len(vals) > 0 {
			return vals[0]
		}
		return ""
	}

	return fm
}
