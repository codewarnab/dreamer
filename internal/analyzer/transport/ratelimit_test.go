package transport

import "testing"

func TestIsRateLimitMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "usage_limit_exceeded",
			msg:  "error: usage_limit_exceeded - you have exceeded your quota",
			want: true,
		},
		{
			name: "usage limit with spaces",
			msg:  "You have hit your usage limit for this month",
			want: true,
		},
		{
			name: "hit your usage limit",
			msg:  "Sorry, you've hit your usage limit. Please upgrade.",
			want: true,
		},
		{
			name: "rate limit",
			msg:  "Rate limit exceeded. Please try again later.",
			want: true,
		},
		{
			name: "rate_limit underscore",
			msg:  "error_code: rate_limit",
			want: true,
		},
		{
			name: "quota exceeded",
			msg:  "Your quota exceeded the allowed limit",
			want: true,
		},
		{
			name: "too many requests",
			msg:  "HTTP 429: Too many requests",
			want: true,
		},
		{
			name: "overloaded (Anthropic)",
			msg:  "The service is currently overloaded. Please retry.",
			want: true,
		},
		{
			name: "negative - unrelated error",
			msg:  "Connection timeout",
			want: false,
		},
		{
			name: "empty string",
			msg:  "",
			want: false,
		},
		{
			name: "case insensitive",
			msg:  "RATE LIMIT EXCEEDED",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRateLimitMessage(tt.msg)
			if got != tt.want {
				t.Errorf("IsRateLimitMessage(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}
