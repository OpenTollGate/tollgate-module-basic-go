package config_manager

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// SetDotPath updates a nested config field using dot-notation and persists to disk.
//
// Examples:
//
//	SetDotPath(cm, "log_level", "debug")                        // top-level string
//	SetDotPath(cm, "accepted_mints.0.price_per_step", "5")      // array → object → uint64
//	SetDotPath(cm, "upstream_detector.probe_timeout", "30s")    // nested duration
//	SetDotPath(cm, "identities.public_identities.0.name", "alice") // identities file
//
// Navigation uses JSON tags (not Go field names) to match the schema keys that the UI uses.
func SetDotPath(cm *ConfigManager, key, value string) error {
	cfg := cm.GetConfig()
	identities := cm.GetIdentities()

	parts := strings.Split(key, ".")
	if len(parts) == 0 {
		return fmt.Errorf("empty key")
	}

	root := parts[0]
	rest := parts[1:]

	switch root {
	case "identities":
		if len(rest) == 0 {
			return fmt.Errorf("cannot replace entire identities object; use specific fields")
		}
		err := setFieldReflect(reflect.ValueOf(identities).Elem(), rest, value)
		if err != nil {
			return err
		}
		return SaveIdentities(cm.IdentitiesFilePath, identities)
	default:
		err := setFieldReflect(reflect.ValueOf(cfg).Elem(), parts, value)
		if err != nil {
			return err
		}
		return SaveConfig(cm.ConfigFilePath, cfg)
	}
}

// setFieldReflect walks a struct hierarchy by path parts using reflection.
// It navigates through structs (by JSON tag), slices (by numeric index),
// and pointers (auto-dereferenced) to reach the leaf field and set its value.
func setFieldReflect(v reflect.Value, parts []string, value string) error {
	for i, part := range parts {
		if !v.IsValid() {
			return fmt.Errorf("invalid path at %s", strings.Join(parts[:i], "."))
		}

		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return fmt.Errorf("nil pointer at %s", strings.Join(parts[:i], "."))
			}
			v = v.Elem()
		}

		isLast := i == len(parts)-1

		if idx, err := strconv.Atoi(part); err == nil && v.Kind() == reflect.Slice {
			if idx < 0 || idx >= v.Len() {
				return fmt.Errorf("index %d out of range (len=%d) at %s", idx, v.Len(), strings.Join(parts[:i], "."))
			}
			v = v.Index(idx)
			if isLast {
				return setFieldValue(v, value)
			}
			continue
		}

		if v.Kind() == reflect.Struct {
			field, found := findFieldByJSONTag(v, part)
			if !found {
				return fmt.Errorf("unknown field %s at %s", part, strings.Join(parts[:i+1], "."))
			}
			v = field
			if isLast {
				return setFieldValue(v, value)
			}
			continue
		}

		return fmt.Errorf("cannot navigate into %s at %s", v.Kind(), strings.Join(parts[:i], "."))
	}

	return fmt.Errorf("empty path")
}

func setFieldValue(v reflect.Value, value string) error {
	if !v.CanSet() {
		return fmt.Errorf("field is not settable")
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid bool value %q: %w", value, err)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int64:
		if v.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("invalid duration %q (use e.g. 10s, 2m, 300ms): %w", value, err)
			}
			v.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer value %q: %w", value, err)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid unsigned integer value %q: %w", value, err)
		}
		v.SetUint(n)
	case reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid float value %q: %w", value, err)
		}
		v.SetFloat(f)
	default:
		return fmt.Errorf("unsupported type %s for set", v.Kind())
	}

	return nil
}

func findFieldByJSONTag(v reflect.Value, tag string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" {
			continue
		}
		parts := strings.Split(jsonTag, ",")
		name := parts[0]
		if name == "" {
			name = field.Name
		}
		if name == tag {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}
