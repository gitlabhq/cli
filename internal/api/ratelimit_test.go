package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	reset := strconv.FormatInt(now.Add(30*time.Second).Unix(), 10)

	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
		wantOK bool
	}{
		{
			name:   "seconds",
			header: http.Header{"Retry-After": {"60"}},
			want:   time.Minute,
			wantOK: true,
		},
		{
			name:   "HTTP date",
			header: http.Header{"Retry-After": {now.Add(90 * time.Second).Format(http.TimeFormat)}},
			want:   90 * time.Second,
			wantOK: true,
		},
		{
			name:   "HTTP date in the past",
			header: http.Header{"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)}},
			want:   0,
			wantOK: true,
		},
		{
			name:   "RateLimit-Reset",
			header: http.Header{"Ratelimit-Reset": {reset}},
			want:   30 * time.Second,
			wantOK: true,
		},
		{
			name: "Retry-After preferred over RateLimit-Reset",
			header: http.Header{
				"Retry-After":     {"5"},
				"Ratelimit-Reset": {reset},
			},
			want:   5 * time.Second,
			wantOK: true,
		},
		{
			name:   "unparseable Retry-After falls back to RateLimit-Reset",
			header: http.Header{"Retry-After": {"soon"}, "Ratelimit-Reset": {reset}},
			want:   30 * time.Second,
			wantOK: true,
		},
		{
			name:   "negative Retry-After",
			header: http.Header{"Retry-After": {"-1"}},
			wantOK: false,
		},
		{
			name:   "no headers",
			header: http.Header{},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RetryAfter(tt.header, now)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRateLimitHint(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		resp *http.Response
		want string
	}{
		{
			name: "named limit",
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": {"60"}, "Ratelimit-Name": {"search_rate_limit"}},
			},
			want: "rate limited by search_rate_limit, retry after 1m0s",
		},
		{
			name: "no name",
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": {"12"}},
			},
			want: "rate limited, retry after 12s",
		},
		{
			name: "no wait",
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Ratelimit-Name": {"search_rate_limit"}},
			},
			want: "",
		},
		{
			name: "not a 429",
			resp: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Retry-After": {"60"}},
			},
			want: "",
		},
		{
			name: "nil response",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RateLimitHint(tt.resp, now))
		})
	}
}
