package config_manager

// Regenerates LuCI UI and CGI from Go struct definitions.
// Run: go generate ./src/config_manager/
//go:generate go run ../../cmd/luci-gen/parser.go ../../cmd/luci-gen/conventions.go ../../cmd/luci-gen/js_generator.go ../../cmd/luci-gen/cgi_generator.go ../../cmd/luci-gen/main.go -path ../..
