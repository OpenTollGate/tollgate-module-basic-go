package config_manager

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

const multiWANFixture = `{"enabled":true,"engine":"mwan3","default_objective":"balanced","allow_change":"next_session","mark_base":"0x100","health_check":{"interval_s":5,"down":3,"up":2,"track_ips":["1.1.1.1"]},"sampler":{"window_s":60,"min_samples":5},"eligibility":{"min_samples":5,"max_loss_pct":10,"weight_max":100},"uplinks":[{"id":"wan","label":"Wired","source":"plain","cost":{"value":0.5,"unit":"sats_mib"}}],"objectives":[{"id":"balanced","label":"Balanced","mode":"balanced","weight_override":{"wan":10}}]}`

func TestMultiWANJSONContract(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"multi_wan":`+multiWANFixture+`}`), &cfg); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var got, want interface{}
	if err := json.Unmarshal(fields["multi_wan"], &got); err != nil {
		t.Fatalf("multi_wan not preserved: %v", err)
	}
	if err := json.Unmarshal([]byte(multiWANFixture), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed contract: %s", fields["multi_wan"])
	}
}

func TestMultiWANOptionalDoesNotChangeCanonicalJSON(t *testing.T) {
	field, ok := reflect.TypeOf(Config{}).FieldByName("MultiWAN")
	if !ok || field.Type.Kind() != reflect.Ptr || field.Tag.Get("json") != "multi_wan,omitempty" {
		t.Fatal("MultiWAN must be an optional pointer with canonical JSON tag")
	}
	for _, cfg := range []*Config{{}, NewDefaultConfig()} {
		before, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(before, []byte(`"multi_wan"`)) {
			t.Fatal("absent multi_wan changed existing JSON")
		}
		// Explicit null must have the same omitted representation as legacy input.
		var loaded Config
		input := append(append([]byte{}, before[:len(before)-1]...), []byte(`,"multi_wan":null}`)...)
		if err := json.Unmarshal(input, &loaded); err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(loaded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("legacy canonical JSON changed:\nbefore %s\nafter %s", before, after)
		}
	}
}

// Return diagnostics so planted mutations can demonstrate that the fence detects drift.
func multiWANSchemaErrors(typ reflect.Type, schema []FieldSchema) []string {
	tags := collectJSONTags(typ, "")
	keys := collectSchemaKeys(schema, "")
	var errors []string
	for key := range tags {
		if _, ok := keys[key]; !ok {
			errors = append(errors, "missing "+key)
		}
	}
	for key := range keys {
		if _, ok := tags[key]; !ok {
			errors = append(errors, "orphan "+key)
		}
	}
	var walk func([]FieldSchema)
	walk = func(fields []FieldSchema) {
		for _, f := range fields {
			if f.Description == "" || !f.Editable {
				errors = append(errors, "missing metadata "+f.JSONKey)
			}
			if (f.Type == "int" || f.Type == "float64") && f.Min == nil {
				errors = append(errors, "missing minimum "+f.JSONKey)
			}
			walk(f.Children)
		}
	}
	walk(schema)
	return errors
}

func TestMultiWANSchemaCoverageAndDrift(t *testing.T) {
	field, ok := reflect.TypeOf(Config{}).FieldByName("MultiWAN")
	if !ok {
		t.Fatal("MultiWAN config field missing")
	}
	var entry *FieldSchema
	schema := GetConfigSchema()
	for i := range schema {
		if schema[i].JSONKey == "multi_wan" {
			entry = &schema[i]
		}
	}
	if entry == nil {
		t.Fatal("multi_wan schema missing")
	}
	if entry.Required || entry.Type != "object" || !entry.Editable || entry.Description == "" {
		t.Fatal("optional multi_wan schema metadata incorrect")
	}
	if errors := multiWANSchemaErrors(field.Type, entry.Children); len(errors) != 0 {
		t.Fatal(errors)
	}
	mutated := append([]FieldSchema{}, entry.Children[1:]...)
	if errors := multiWANSchemaErrors(field.Type, mutated); len(errors) == 0 {
		t.Fatal("missing-field drift escaped fence")
	}
	mutated = append(append([]FieldSchema{}, entry.Children...), FieldSchema{JSONKey: "unknown", Type: "string", Description: "planted", Editable: true})
	if errors := multiWANSchemaErrors(field.Type, mutated); len(errors) == 0 {
		t.Fatal("orphan drift escaped fence")
	}
	for _, key := range []string{"engine", "allow_change"} {
		found := false
		for _, f := range entry.Children {
			if f.JSONKey == key {
				found = len(f.Enum) > 0
			}
		}
		if !found {
			t.Errorf("%s must document supported values", key)
		}
	}
	// Nested pointer/array paths must remain visible to the reflection fence.
	tags := collectJSONTags(field.Type, "")
	for _, key := range []string{"uplinks[].cost.value", "objectives[].weight_override", "health_check.track_ips"} {
		if _, ok := tags[key]; !ok {
			t.Errorf("missing nested path %s; got %v", key, tags)
		}
	}
}
