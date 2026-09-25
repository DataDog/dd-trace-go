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

type fakeResponseWriter struct {
	mu      sync.Mutex
	status  int
	body    []byte
	headers http.Header

	// sendBlock delivers a staged block response to the gateway. It is only armed
	// while a gateway message is in flight: outside of one there is nothing to
	// respond on, and integrations resolve the target message from sendBlockCtx.
	sendBlock    func(context.Context, BlockActionOptions) error
	sendBlockCtx context.Context
	blockArmed   bool
	// blockSent and blockErr record the outcome of the single sendBlock call.
	blockSent bool
	blockErr  error
}

// Reset resets the fakeResponseWriter to its initial state
func (w *fakeResponseWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = 0
	w.body = nil
	w.headers = make(http.Header)
	w.blockSent = false
	w.blockErr = nil
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

// errBlockResponseNotSent reports a block decision that never reached the gateway.
var errBlockResponseNotSent = errors.New("proxy block response was not sent")

// armBlockDelivery allows one block response to be delivered on the gateway
// message that ctx identifies, and returns the matching disarm function.
func (w *fakeResponseWriter) armBlockDelivery(ctx context.Context, send func(context.Context, BlockActionOptions) error) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sendBlock, w.sendBlockCtx, w.blockArmed = send, ctx, true
	return w.disarmBlockDelivery
}

func (w *fakeResponseWriter) disarmBlockDelivery() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockArmed = false
}

// AppSecCommitBlockResponse delivers the staged block response exactly once. AppSec
// calls it right after the block handler wrote to this writer, so the delivery
// outcome is known before the request telemetry is submitted.
func (w *fakeResponseWriter) AppSecCommitBlockResponse() error {
	w.mu.Lock()
	if w.blockSent || !w.blockArmed || w.sendBlock == nil {
		defer w.mu.Unlock()
		if !w.blockSent {
			w.blockErr = errBlockResponseNotSent
		}
		return w.blockErr
	}

	w.blockSent = true
	send, ctx := w.sendBlock, w.sendBlockCtx
	opts := BlockActionOptions{StatusCode: w.status, Headers: w.headers, Body: w.body}
	w.mu.Unlock()

	err := send(ctx, opts)

	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockErr = err
	return err
}

// blockResponseError reports why the staged block response did not reach the gateway.
func (w *fakeResponseWriter) blockResponseError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.blockErr != nil {
		return fmt.Errorf("error creating block message: %w", w.blockErr)
	}
	if !w.blockSent {
		return errBlockResponseNotSent
	}
	return nil
}

var _ http.ResponseWriter = &fakeResponseWriter{}

// newFakeResponseWriter creates a new fakeResponseWriter that can be used to store the response a [http.Handler] made
func newFakeResponseWriter() *fakeResponseWriter {
	return &fakeResponseWriter{
		headers: make(http.Header),
	}
}
