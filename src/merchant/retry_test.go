package merchant

import (
	"errors"
	"testing"
)

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"429 status code", errors.New("HTTP 429 Too Many Requests"), true},
		{"rate limit message", errors.New("rate limit exceeded"), true},
		{"Too Many Requests", errors.New("Too Many Requests"), true},
		{"mixed case Rate Limited", errors.New("Rate Limited by server"), true},
		{"generic error", errors.New("connection refused"), false},
		{"token spent", errors.New("token already spent"), false},
		{"network timeout", errors.New("context deadline exceeded"), false},
		{"empty message", errors.New(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRateLimitError(tt.err)
			if got != tt.want {
				t.Errorf("isRateLimitError(%q) = %v, want %v", tt.err.Error(), got, tt.want)
			}
		})
	}
}
