// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package message_processor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// NewRequest is a function that creates a new http.Request from the given parameters.
func NewRequest(ctx context.Context, scheme, authority, path, method string, headers http.Header, remoteAddr string, tlsState *tls.ConnectionState) (*http.Request, error) {
	parsedURL, err := urlParse(scheme, authority, path)
	if err != nil {
		return nil, err
	}

	return (&http.Request{
		Method:     method,
		Host:       authority,
		RequestURI: path,
		URL:        parsedURL,
		Header:     headers,
		RemoteAddr: remoteAddr,
		TLS:        tlsState,
	}).WithContext(ctx), nil
}

func urlParse(scheme, authority, rest string) (*url.URL, error) {
	var escapeErr url.EscapeError

	// Parse the URL from the scheme, authority and path
	parsedURL, err := url.Parse(fmt.Sprintf("%s://%s%s", scheme, authority, rest))
	for i := 0; i < 5 && errors.As(err, &escapeErr); i++ {
		// If an unknown escape sequence is found, we try to escape the path again by adding a % in front
		i := strings.Index(rest, string(escapeErr)) // This is to trigger the escape error
		if i < 0 {
			return nil, fmt.Errorf("error parsing URL: %w", err)
		}

		rest = rest[:i] + "%25" + rest[i+1:]
		parsedURL, err = url.Parse(fmt.Sprintf("%s://%s%s", scheme, authority, rest))
	}

	if err != nil {
		return nil, fmt.Errorf(
			"error building envoy URI from scheme %q, from host %q and from path %q: %w",
			scheme,
			authority,
			rest,
			err)
	}
	return parsedURL, nil
}

type fakeResponseWriter struct {
	mu      sync.Mutex
	status  int
	body    []byte
	headers http.Header
}

// Reset resets the fakeResponseWriter to its initial state
func (w *fakeResponseWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = 0
	w.body = nil
	w.headers = make(http.Header)
}

// Status is not in the [http.ResponseWriter] interface, but it is cast into it by the tracing code
func (w *fakeResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *fakeResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}

func (w *fakeResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.headers
}

func (w *fakeResponseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.body = append(w.body, b...)
	return len(b), nil
}

var _ http.ResponseWriter = &fakeResponseWriter{}

// newFakeResponseWriter creates a new fakeResponseWriter that can be used to store the response a [http.Handler] made
func newFakeResponseWriter() *fakeResponseWriter {
	return &fakeResponseWriter{
		headers: make(http.Header),
	}
}
