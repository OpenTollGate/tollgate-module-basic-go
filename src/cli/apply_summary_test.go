package cli

import "testing"

func TestApplySummaryCountsUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		results []SettingResult
		want    string
	}{
		{
			name: "unchanged is counted alongside skipped",
			results: []SettingResult{
				{Setting: "admin_access", Status: statusUnchanged},
				{Setting: "private_encryption", Status: statusSkipped},
			},
			want: "runtime: 1 unchanged, 1 not applicable here",
		},
		{
			name: "all unchanged now reports a counted summary",
			results: []SettingResult{
				{Setting: "private_ssid", Status: statusUnchanged},
				{Setting: "admin_access", Status: statusUnchanged},
			},
			want: "runtime: 2 unchanged",
		},
		{
			name: "applied sorts before unchanged",
			results: []SettingResult{
				{Setting: "private_ssid", Status: statusUnchanged},
				{Setting: "admin_access", Status: statusApplied},
			},
			want: "runtime: 1 applied, 1 unchanged",
		},
		{
			name:    "no reportable rows keeps the already-matches message",
			results: nil,
			want:    "runtime already matches",
		},
		{
			name: "full counter order matches the operator guide",
			results: []SettingResult{
				{Setting: "a", Status: statusSkipped},
				{Setting: "b", Status: statusFailed},
				{Setting: "c", Status: statusRefused},
				{Setting: "d", Status: statusUnchanged},
				{Setting: "e", Status: statusApplied},
			},
			want: "runtime: 1 applied, 1 unchanged, 1 refused, 1 failed, 1 not applicable here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applySummary(tt.results); got != tt.want {
				t.Errorf("applySummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
