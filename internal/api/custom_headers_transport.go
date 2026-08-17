package api

import (
	"net/http"
	"strings"
)

type customHeadersTransport struct {
	rt           http.RoundTripper
	headers      map[string]string
	allowedHosts map[string]struct{}
}

func (t *customHeadersTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := t.allowedHosts[strings.ToLower(req.URL.Host)]; !ok {
		return t.rt.RoundTrip(req)
	}

	req = req.Clone(req.Context())
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for name, value := range t.headers {
		// Configured custom headers are authoritative. This preserves the prior
		// go-gitlab WithHeaders behavior and prevents duplicate proxy credentials.
		req.Header.Set(name, value)
	}
	return t.rt.RoundTrip(req)
}
