package config_manager

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func SetDotPath(cm *ConfigManager, key, value string) error {
	if err := validateAgainstSchema(key, value); err != nil {
		return fmt.Errorf("validation failed for %q: %w", key, err)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	cfg := cm.config
	identities := cm.identitiesConfig

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

func validateAgainstSchema(key, value string) error {
	parts := strings.Split(key, ".")
	if len(parts) == 0 {
		return fmt.Errorf("empty key")
	}

	root := parts[0]
	rest := parts[1:]

	var schema []FieldSchema
	if root == "identities" {
		schema = GetIdentitiesSchema()
		rest = parts[1:]
	} else {
		schema = GetConfigSchema()
		rest = parts
	}

	var field *FieldSchema
	currentSchema := schema
	for i, part := range rest {
		if _, err := strconv.Atoi(part); err == nil {
			if field != nil && (field.Type == "array") && len(field.Children) > 0 {
				currentSchema = field.Children
			}
			continue
		}
		found := false
		for idx := range currentSchema {
			if currentSchema[idx].JSONKey == part {
				field = &currentSchema[idx]
				found = true
				if i < len(rest)-1 && len(field.Children) > 0 {
					currentSchema = field.Children
				}
				break
			}
		}
		if !found {
			return nil
		}
	}

	if field == nil {
		return nil
	}

	if len(field.Enum) > 0 {
		for _, e := range field.Enum {
			if e == value {
				return nil
			}
		}
		return fmt.Errorf("value %q not in allowed values: %v", value, field.Enum)
	}

	switch field.Type {
	case "uint64":
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid uint64 value %q", value)
		}
		if field.Min != nil {
			if min, ok := field.Min.(uint64); ok && n < min {
				return fmt.Errorf("value %d is below minimum %d", n, min)
			}
		}
		if field.Max != nil {
			if max, ok := field.Max.(uint64); ok && n > max {
				return fmt.Errorf("value %d exceeds maximum %d", n, max)
			}
		}
	case "int":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid int value %q", value)
		}
		if field.Min != nil {
			if min, ok := toInt64(field.Min); ok && n < min {
				return fmt.Errorf("value %d is below minimum %d", n, min)
			}
		}
		if field.Max != nil {
			if max, ok := toInt64(field.Max); ok && n > max {
				return fmt.Errorf("value %d exceeds maximum %d", n, max)
			}
		}
	case "float64":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid float64 value %q", value)
		}
		if field.Min != nil {
			if min, ok := toFloat64(field.Min); ok && f < min {
				return fmt.Errorf("value %f is below minimum %f", f, min)
			}
		}
		if field.Max != nil {
			if max, ok := toFloat64(field.Max); ok && f > max {
				return fmt.Errorf("value %f exceeds maximum %f", f, max)
			}
		}
	}

	return nil
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

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
