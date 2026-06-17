package upstream_session_manager

import (
	"strings"
	"testing"
)

func TestParseUsageResponse(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantUsage     uint64
		wantAllotment uint64
		wantErr       bool
		errContains   string
	}{
		{
			name:          "normal values",
			body:          "100/200",
			wantUsage:     100,
			wantAllotment: 200,
		},
		{
			name:          "normal values with whitespace",
			body:          "  1048576/10485760  ",
			wantUsage:     1048576,
			wantAllotment: 10485760,
		},
		{
			name:          "no session -1/-1 returns zero",
			body:          "-1/-1",
			wantUsage:     0,
			wantAllotment: 0,
		},
		{
			name:        "negative usage positive allotment",
			body:        "-1/100",
			wantErr:     true,
			errContains: "negative usage/allotment from upstream: -1/100",
		},
		{
			name:        "positive usage negative allotment",
			body:        "50/-1",
			wantErr:     true,
			errContains: "negative usage/allotment from upstream: 50/-1",
		},
		{
			name:        "both negative not -1/-1",
			body:        "-2/-3",
			wantErr:     true,
			errContains: "negative usage/allotment from upstream: -2/-3",
		},
		{
			name:        "large negative usage",
			body:        "-999999/100",
			wantErr:     true,
			errContains: "negative usage/allotment from upstream: -999999/100",
		},
		{
			name:          "zero values",
			body:          "0/0",
			wantUsage:     0,
			wantAllotment: 0,
		},
		{
			name:        "invalid usage value",
			body:        "not-a-number/200",
			wantErr:     true,
			errContains: "invalid usage value",
		},
		{
			name:        "invalid allotment value",
			body:        "100/xyz",
			wantErr:     true,
			errContains: "invalid allotment value",
		},
		{
			name:        "missing slash",
			body:        "100200",
			wantErr:     true,
			errContains: "invalid usage response format",
		},
		{
			name:        "empty body",
			body:        "",
			wantErr:     true,
			errContains: "invalid usage response format",
		},
		{
			name:        "too many slashes",
			body:        "100/200/300",
			wantErr:     true,
			errContains: "invalid usage response format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, allotment, err := parseUsageResponse(tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if usage != tt.wantUsage {
				t.Errorf("usage = %d, want %d", usage, tt.wantUsage)
			}
			if allotment != tt.wantAllotment {
				t.Errorf("allotment = %d, want %d", allotment, tt.wantAllotment)
			}
		})
	}
}
