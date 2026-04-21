//go:build ignore

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	basePath := flag.String("path", ".", "project root directory")
	flag.Parse()

	structs := parseStructs(*basePath)
	if len(structs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no structs parsed")
		os.Exit(1)
	}

	fmt.Printf("Parsed %d structs:\n", len(structs))
	for name, sd := range structs {
		fmt.Printf("  %s (%d fields)\n", name, len(sd.Fields))
	}

	schema := buildUISchema(structs)
	fmt.Printf("\nUI Schema:\n")
	fmt.Printf("  Top-level fields: %d\n", len(schema.TopLevelFields))
	fmt.Printf("  Sections: %d\n", len(schema.Sections))
	for _, sec := range schema.Sections {
		fmt.Printf("    %s (json:%s, array:%v, fields:%d)\n", sec.Name, sec.JSONKey, sec.IsArray, len(sec.Fields))
	}

	js := generateJS(schema, structs)
	jsPath := filepath.Join(*basePath, "files/www/luci-static/resources/view/tollgate-payments/settings.js")
	if err := os.WriteFile(jsPath, []byte(js), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JS: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nGenerated: %s (%d bytes)\n", jsPath, len(js))

	cgi := generateCGI(schema, structs)
	cgiPath := filepath.Join(*basePath, "files/www/cgi-bin/tollgate-api")
	if err := os.WriteFile(cgiPath, []byte(cgi), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CGI: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated: %s (%d bytes)\n", cgiPath, len(cgi))

	fmt.Println("\nDone.")
}
