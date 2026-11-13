package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// RetryAfter returns how long a rate-limited response asks the caller to wait,
// from Retry-After (seconds or an HTTP date) or else RateLimit-Reset (a Unix
// time).
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second, true
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(t.Sub(now), 0), true
		}
	}
	if v := h.Get("RateLimit-Reset"); v != "" {
		if reset, err := strconv.ParseInt(v, 10, 64); err == nil && reset > 0 {
			return max(time.Unix(reset, 0).Sub(now), 0), true
		}
	}
	return 0, false
}

// RateLimitHint describes an HTTP 429 response for an error message, for
// example "rate limited by search_rate_limit, retry after 1m0s". It returns ""
// for any other response, or when the response doesn't say how long to wait.
func RateLimitHint(resp *http.Response, now time.Time) string {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}
	wait, ok := RetryAfter(resp.Header, now)
	if !ok {
		return ""
	}
	limited := "rate limited"
	if name := resp.Header.Get("RateLimit-Name"); name != "" {
		limited += " by " + name
	}
	return fmt.Sprintf("%s, retry after %s", limited, wait.Round(time.Second))
}
