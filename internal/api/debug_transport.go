package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

var sensitiveHeaders = []string{
	gitlab.AccessTokenHeaderName,
	gitlab.JobTokenHeaderName,
	"Authorization",
	"Proxy-Authorization",
	"Cf-Access-Client-Secret",
}

type debugTransport struct {
	rt http.RoundTripper
	w  io.Writer
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqToDump, err := redactKnownSensitiveHeaders(req)
	if err != nil {
		return nil, err
	}

	reqDump, err := httputil.DumpRequestOut(reqToDump, true)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(d.w, "REQUEST:\n%s\n\n", reqDump)

	// Do request
	resp, err := d.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Dump response
	respDump, err := httputil.DumpResponse(resp, true)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(d.w, "RESPONSE:\n%s\n\n", respDump)

	return resp, nil
}

func redactKnownSensitiveHeaders(req *http.Request) (*http.Request, error) {
	r1, r2, err := drainBody(req.Body)
	if err != nil {
		return nil, err
	}

	cloned := req.Clone(context.Background())
	cloned.Body = r1
	req.Body = r2

	for _, h := range sensitiveHeaders {
		if cloned.Header.Get(h) != "" {
			cloned.Header.Set(h, "[REDACTED]")
		}
	}
	return cloned, nil
}

// The code below is copied from net/http/httputil/dump.go
// Copyright 2009 The Go Authors. All rights reserved.

// drainBody reads all of b to memory and then returns two equivalent
// ReadClosers yielding the same bytes.
//
// It returns an error if the initial slurp of all bytes fails. It does not attempt
// to make the returned ReadClosers have identical error-matching behavior.
func drainBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) { //nolint:nonamedreturns
	if b == nil || b == http.NoBody {
		// No copying needed. Preserve the magic sentinel meaning of NoBody.
		return http.NoBody, http.NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err = b.Close(); err != nil {
		return nil, b, err
	}
	return io.NopCloser(&buf), io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}
