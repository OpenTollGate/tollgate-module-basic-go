package config_manager

import (
	"encoding/json"
	"testing"
	"time"
)

// The shipped defaults live in two tables that must never drift apart:
// NewDefaultConfig (what a fresh install gets) and the Default fields of
// GetConfigSchema (what the config UI and schema consumers present). The
// lenient-defaults change (#478) had to update both by hand — this test
// makes that lockstep mechanical: any leaf schema Default that disagrees
// with the JSON of NewDefaultConfig fails here.
func TestSchemaDefaultsMatchNewDefaultConfig(t *testing.T) {
	var defaultsJSON map[string]interface{}
	raw, err := json.Marshal(NewDefaultConfig())
	if err != nil {
		t.Fatalf("marshal NewDefaultConfig: %v", err)
	}
	if err := json.Unmarshal(raw, &defaultsJSON); err != nil {
		t.Fatalf("unmarshal NewDefaultConfig: %v", err)
	}

	var walk func(prefix string, fields []FieldSchema, node map[string]interface{})
	walk = func(prefix string, fields []FieldSchema, node map[string]interface{}) {
		for _, f := range fields {
			if len(f.Children) > 0 {
				child, ok := node[f.JSONKey].(map[string]interface{})
				if !ok {
					// List-shaped fields (arrays of objects/strings) model
					// their item shape in Children; the shipped default is
					// an array, which carries no per-item lockstep claim.
					continue
				}
				walk(prefix+f.Name+".", f.Children, child)
				continue
			}
			if f.Default == nil {
				continue // no declared default: nothing to drift
			}
			want, err := json.Marshal(f.Default)
			if err != nil {
				t.Fatalf("marshal schema default: %v", err)
			}
			got, ok := node[f.JSONKey]
			if !ok {
				// omitempty round-trip: an absent key is consistent with a
				// zero-valued declared default; anything else is drift.
				if normalize(t, want) == "0" || normalize(t, want) == "" {
					continue
				}
				t.Errorf("%s%s: schema declares default %v for %q, but NewDefaultConfig omits the key", prefix, f.Name, f.Default, f.JSONKey)
				continue
			}
			gotJSON, _ := json.Marshal(got)
			if normalize(t, gotJSON) != normalize(t, want) {
				t.Errorf("%s%s (%s): schema default %s != shipped default %s", prefix, f.Name, f.JSONKey, want, gotJSON)
			}
		}
	}
	walk("", GetConfigSchema(), defaultsJSON)
}

// normalize renders both sides of a comparison in one encoding: numbers
// plainly, and duration-like strings ("10s", "500ms") as their nanosecond
// count, because the schema presents durations human-readable while the
// struct marshals time.Duration as an integer.
func normalize(t *testing.T, b []byte) string {
	t.Helper()
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		return trimFloat(f)
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if d, err := time.ParseDuration(s); err == nil {
			return trimFloat(float64(d))
		}
		return s
	}
	return string(b)
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
