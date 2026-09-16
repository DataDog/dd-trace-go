// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
)

// PseudoRequest represents the pseudo headers of an HTTP request.
type PseudoRequest struct {
	Scheme     string
	Authority  string
	Path       string
	Method     string
	RemoteAddr string
	Headers    map[string][]string

	// ClientIP is the identity reported by the proxy's infrastructure. When
	// invalid, the default policy runs against Headers and the raw RemoteAddr.
	ClientIP netip.Addr
}

func (pr PseudoRequest) toNetHTTP(ctx context.Context) (*http.Request, error) {
	parsedURL, err := urlParse(pr.Scheme, pr.Authority, pr.Path)
	if err != nil {
		return nil, err
	}

	var tlsState *tls.ConnectionState
	if pr.Scheme == "https" {
		tlsState = &tls.ConnectionState{}
	}

	return (&http.Request{
		Method:     pr.Method,
		Host:       pr.Authority,
		RequestURI: pr.Path,
		URL:        parsedURL,
		Header:     pr.Headers,
		RemoteAddr: pr.RemoteAddr,
		TLS:        tlsState,
	}).WithContext(ctx), nil
}

func urlParse(scheme, authority, rest string) (*url.URL, error) {
	if scheme == "" {
		scheme = "http"
	}

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

// PseudoResponse represents the pseudo headers of an HTTP response.
type PseudoResponse struct {
	StatusCode int
	Headers    map[string][]string
}

func (pr PseudoResponse) toNetHTTP(rw http.ResponseWriter) {
	for k, v := range pr.Headers {
		for _, vv := range v {
			rw.Header().Add(k, vv)
		}
	}

	rw.WriteHeader(pr.StatusCode)
}

var (
	errBlockMessageFuncUnavailable = errors.New("proxy block response function is unavailable")
	errBlockResponseNotDeliverable = errors.New("proxy block response cannot be delivered outside message processing")
)

type blockMessageState struct {
	ctx     context.Context
	send    func(context.Context, BlockActionOptions) error
	enabled bool
	sent    bool
	err     error
}

type fakeResponseWriter struct {
	mu      sync.Mutex
	status  int
	body    []byte
	headers http.Header

	blockMessage blockMessageState
}

// Reset resets the fakeResponseWriter to its initial state
func (w *fakeResponseWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = 0
	w.body = nil
	w.headers = make(http.Header)
	w.blockMessage.sent = false
	w.blockMessage.err = nil
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

func (w *fakeResponseWriter) setBlockMessageFunc(blockMessageFunc func(context.Context, BlockActionOptions) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockMessage.send = blockMessageFunc
}

func (w *fakeResponseWriter) enableBlockMessages(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockMessage.ctx = ctx
	w.blockMessage.enabled = true
}

func (w *fakeResponseWriter) disableBlockMessages() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockMessage.enabled = false
}

// AppSecCommitBlockResponse constructs the proxy block response once.
func (w *fakeResponseWriter) AppSecCommitBlockResponse() error {
	w.mu.Lock()
	if w.blockMessage.sent {
		err := w.blockMessage.err
		w.mu.Unlock()
		return err
	}
	if w.blockMessage.send == nil {
		w.blockMessage.err = errBlockMessageFuncUnavailable
		w.mu.Unlock()
		return errBlockMessageFuncUnavailable
	}
	if !w.blockMessage.enabled {
		w.blockMessage.err = errBlockResponseNotDeliverable
		w.mu.Unlock()
		return errBlockResponseNotDeliverable
	}

	w.blockMessage.sent = true
	blockMessageFunc := w.blockMessage.send
	ctx := w.blockMessage.ctx
	opts := BlockActionOptions{
		StatusCode: w.status,
		Headers:    w.headers,
		Body:       w.body,
	}
	w.mu.Unlock()

	err := blockMessageFunc(ctx, opts)
	w.mu.Lock()
	w.blockMessage.err = err
	w.mu.Unlock()
	return err
}

func (w *fakeResponseWriter) blockResponseResult() (sent bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.blockMessage.sent, w.blockMessage.err
}

var _ http.ResponseWriter = &fakeResponseWriter{}

// newFakeResponseWriter creates a new fakeResponseWriter that can be used to store the response a [http.Handler] made
func newFakeResponseWriter() *fakeResponseWriter {
	return &fakeResponseWriter{
		headers: make(http.Header),
	}
}
