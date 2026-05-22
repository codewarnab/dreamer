package transport

import "testing"

func TestIsRateLimitMessage(t *testing.T) {
	tests := []struct {
		name        string
		messageText string
		want        bool
	}{
		{
			name: "usage_limit_exceeded",
			messageText: "error: usage_limit_exceeded - you have exceeded your quota",
			want: true,
		},
		{
			name: "usage limit with spaces",
			messageText: "You have hit your usage limit for this month",
			want: true,
		},
		{
			name: "hit your usage limit",
			messageText: "Sorry, you've hit your usage limit. Please upgrade.",
			want: true,
		},
		{
			name: "rate limit",
			messageText: "Rate limit exceeded. Please try again later.",
			want: true,
		},
		{
			name: "rate_limit underscore",
			messageText: "error_code: rate_limit",
			want: true,
		},
		{
			name: "quota exceeded",
			messageText: "Your quota exceeded the allowed limit",
			want: true,
		},
		{
			name: "too many requests",
			messageText: "HTTP 429: Too many requests",
			want: true,
		},
		{
			name: "overloaded (Anthropic)",
			messageText: "The service is currently overloaded. Please retry.",
			want: true,
		},
		{
			name: "negative - unrelated error",
			messageText: "Connection timeout",
			want: false,
		},
		{
			name: "empty string",
			messageText: "",
			want: false,
		},
		{
			name: "case insensitive",
			messageText: "RATE LIMIT EXCEEDED",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRateLimitMessage(tt.messageText)
			if got != tt.want {
				t.Errorf("IsRateLimitMessage(%q) = %v, want %v", tt.messageText, got, tt.want)
			}
		})
	}
}
