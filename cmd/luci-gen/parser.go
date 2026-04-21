package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// StructDef holds a parsed Go struct definition.
type StructDef struct {
	Name   string
	Fields []FieldDef
}

// FieldDef holds a parsed Go struct field.
type FieldDef struct {
	GoName    string // e.g. "LogLevel"
	GoType    string // e.g. "string", "uint64", "time.Duration", "[]MintConfig"
	JSONKey   string // e.g. "log_level"
	Omitempty bool
}

func parseStructs(basePath string) map[string]*StructDef {
	result := make(map[string]*StructDef)

	files := []string{
		filepath.Join(basePath, "src/config_manager/config_manager_config.go"),
		filepath.Join(basePath, "src/config_manager/config_manager_identities.go"),
	}

	for _, filePath := range files {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", filePath, err)
			continue
		}

		ast.Inspect(node, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Type == nil {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}

			sd := &StructDef{Name: ts.Name.Name}

			for _, f := range st.Fields.List {
				if len(f.Names) == 0 {
					continue
				}
				fd := FieldDef{
					GoName: f.Names[0].Name,
					GoType: exprTypeStr(f.Type),
				}

				if f.Tag != nil {
					tagStr := strings.Trim(f.Tag.Value, "`")
					tag := reflect.StructTag(tagStr)
					if jt, ok := tag.Lookup("json"); ok {
						parts := strings.Split(jt, ",")
						fd.JSONKey = parts[0]
						for _, p := range parts[1:] {
							if p == "omitempty" {
								fd.Omitempty = true
							}
						}
					}
				}

				sd.Fields = append(sd.Fields, fd)
			}

			result[sd.Name] = sd
			return true
		})
	}

	return result
}

func exprTypeStr(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprTypeStr(v.X) + "." + v.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprTypeStr(v.Elt)
	case *ast.StarExpr:
		return "*" + exprTypeStr(v.X)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.MapType:
		return "map[" + exprTypeStr(v.Key) + "]" + exprTypeStr(v.Value)
	default:
		return fmt.Sprintf("<%T>", e)
	}
}
