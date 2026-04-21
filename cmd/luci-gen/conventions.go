package main

import "strings"

// InputType represents the kind of HTML input element to use.
type InputType int

const (
	InputText InputType = iota
	InputNumber
	InputNumberFloat
	InputNumberDuration
	InputCheckbox
	InputTextareaLines
	InputSelect
	InputCardSection  // []StructType → repeatable cards
	InputSection      // nested struct → sub-section
)

// UIField describes how a single Go struct field maps to the UI.
type UIField struct {
	GoName     string
	GoType     string
	JSONKey    string   // e.g. "probe_timeout"
	JSONPath   string   // full dot-path from config root, e.g. "upstream_detector.probe_timeout"
	InputType  InputType
	Label      string   // "Probe timeout (s)"
	HTMLID     string   // "probe_timeout_s"
	CSSClass   string   // "mint-url" (for card fields)
	Omitempty  bool
	SelectOpts []string
	Default    string   // JS literal default, e.g. "64", "''"
}

// UISection groups fields under a heading.
type UISection struct {
	Name       string   // "Accepted mints", "Upstream detector"
	JSONKey    string   // "accepted_mints"
	StructName string   // "MintConfig"
	IsArray    bool
	Fields     []UIField
}

// UISchema is the complete layout for the configuration UI.
type UISchema struct {
	TopLevelFields []UIField
	Sections       []UISection
}

// ── enum mappings ─────────────────────────────────────────

var enumMap = map[string][]string{
	"metric":         {"milliseconds", "bytes"},
	"log_level":      {"debug", "info", "warn", "error"},
	"default_policy": {"trust_all", "trust_none"},
}

// ── helpers ────────────────────────────────────────────────

// makeLabel turns a json_key into a human label: "log_level" → "Log level"
func makeLabel(jsonKey string) string {
	s := strings.ReplaceAll(jsonKey, "_", " ")
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// shouldSkipField returns true for fields that must never appear in the UI.
func shouldSkipField(structName, jsonKey string) bool {
	return structName == "OwnedIdentity" && jsonKey == "privatekey"
}

// jsDefault returns a JS literal for the default value of a field.
func jsDefault(goType, jsonKey string) string {
	switch goType {
	case "string":
		return "''"
	case "uint64", "int":
		switch jsonKey {
		case "min_balance":
			return "64"
		case "balance_tolerance_percent":
			return "10"
		case "payout_interval_seconds":
			return "60"
		case "min_payout_amount":
			return "128"
		case "price_per_step":
			return "1"
		default:
			return "0"
		}
	case "float64":
		return "0"
	case "time.Duration":
		return "0"
	case "bool":
		return "false"
	default:
		return "''"
	}
}

// deriveCardPrefix maps a section JSON key to the CSS class prefix.
func deriveCardPrefix(sectionJSONKey string) string {
	switch sectionJSONKey {
	case "accepted_mints":
		return "mint"
	case "profit_share":
		return "share"
	case "public_identities":
		return "ident"
	default:
		parts := strings.Split(sectionJSONKey, "_")
		if len(parts) > 0 {
			return parts[0]
		}
		return sectionJSONKey
	}
}

// capitalizeUpper capitalizes the first letter.
func capitalizeUpper(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ── build UIField from parsed FieldDef ────────────────────

func buildUIField(fd FieldDef, structs map[string]*StructDef) UIField {
	f := UIField{
		GoName:    fd.GoName,
		GoType:    fd.GoType,
		JSONKey:   fd.JSONKey,
		Omitempty: fd.Omitempty,
		Default:   jsDefault(fd.GoType, fd.JSONKey),
	}

	switch fd.GoType {
	case "string":
		if opts, ok := enumMap[fd.JSONKey]; ok {
			f.InputType = InputSelect
			f.SelectOpts = opts
		} else {
			f.InputType = InputText
		}
	case "bool":
		f.InputType = InputCheckbox
	case "uint64", "int":
		f.InputType = InputNumber
	case "float64":
		f.InputType = InputNumberFloat
	case "time.Duration":
		f.InputType = InputNumberDuration
	case "[]string":
		f.InputType = InputTextareaLines
	default:
		if strings.HasPrefix(fd.GoType, "[]") {
			elemType := fd.GoType[2:]
			if _, ok := structs[elemType]; ok {
				f.InputType = InputCardSection
			} else {
				f.InputType = InputTextareaLines
			}
		} else if _, ok := structs[fd.GoType]; ok {
			f.InputType = InputSection
		} else {
			f.InputType = InputText
		}
	}

	f.Label = makeLabel(fd.JSONKey)
	if fd.GoType == "time.Duration" {
		f.Label += " (s)"
	}

	f.HTMLID = fd.JSONKey
	if fd.GoType == "time.Duration" {
		f.HTMLID = fd.JSONKey + "_s"
	}

	return f
}

// ── build flattened fields for a section ──────────────────

// buildSectionFields recursively flattens nested structs into a single field list.
// cardPrefix is the CSS class prefix for card sections (empty for flat sections).
// pathPrefix is the dot-separated JSON path up to (but not including) this struct's fields.
func buildSectionFields(sd *StructDef, structs map[string]*StructDef, cardPrefix, pathPrefix string) []UIField {
	var fields []UIField
	for _, fd := range sd.Fields {
		if shouldSkipField(sd.Name, fd.JSONKey) {
			continue
		}
		uiField := buildUIField(fd, structs)
		uiField.JSONPath = pathPrefix + fd.JSONKey

		if cardPrefix != "" {
			uiField.CSSClass = cardPrefix + "-" + fd.JSONKey
		}

		// If the field is a nested struct (non-array), flatten its fields recursively
		if uiField.InputType == InputSection {
			subSD := structs[fd.GoType]
			if subSD != nil {
				subFields := buildSectionFields(subSD, structs, cardPrefix, pathPrefix+fd.JSONKey+".")
				fields = append(fields, subFields...)
				continue
			}
		}

		fields = append(fields, uiField)
	}
	return fields
}

// ── build full UISchema ───────────────────────────────────

func buildUISchema(structs map[string]*StructDef) *UISchema {
	schema := &UISchema{}

	config, ok := structs["Config"]
	if !ok {
		return schema
	}

	for _, fd := range config.Fields {
		uiField := buildUIField(fd, structs)
		uiField.JSONPath = fd.JSONKey

		switch uiField.InputType {
		case InputCardSection:
			elemType := fd.GoType[2:]
			sd := structs[elemType]
			section := UISection{
				Name:       makeLabel(fd.JSONKey),
				JSONKey:    fd.JSONKey,
				StructName: elemType,
				IsArray:    true,
			}
			prefix := deriveCardPrefix(fd.JSONKey)
			section.Fields = buildSectionFields(sd, structs, prefix, "")
			schema.Sections = append(schema.Sections, section)

		case InputSection:
			sd := structs[fd.GoType]
			section := UISection{
				Name:       makeLabel(fd.JSONKey),
				JSONKey:    fd.JSONKey,
				StructName: fd.GoType,
				IsArray:    false,
			}
			section.Fields = buildSectionFields(sd, structs, "", fd.JSONKey+".")
			schema.Sections = append(schema.Sections, section)

		default:
			schema.TopLevelFields = append(schema.TopLevelFields, uiField)
		}
	}

	return schema
}
