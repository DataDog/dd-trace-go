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

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
)

type clientIPState uint8

const (
	clientIPAbsent clientIPState = iota
	clientIPValid
	clientIPInvalid
)

// ClientIPResolution is an authoritative client IP result. Its zero value means absent.
type ClientIPResolution struct {
	addr  netip.Addr
	state clientIPState
}

// NewClientIPResolution returns an authoritative valid client IP result.
func NewClientIPResolution(addr netip.Addr) ClientIPResolution {
	if !addr.IsValid() || addr.Zone() != "" {
		return InvalidClientIPResolution()
	}
	return ClientIPResolution{addr: addr.Unmap(), state: clientIPValid}
}

// InvalidClientIPResolution returns an authoritative invalid client IP result.
func InvalidClientIPResolution() ClientIPResolution {
	return ClientIPResolution{state: clientIPInvalid}
}

func (ip ClientIPResolution) value() (netip.Addr, bool) {
	switch ip.state {
	case clientIPAbsent:
		return netip.Addr{}, false
	case clientIPValid:
		return ip.addr, true
	case clientIPInvalid:
		return netip.Addr{}, true
	default:
		return netip.Addr{}, false
	}
}

// PseudoRequest represents the pseudo headers of an HTTP request.
type PseudoRequest struct {
	Scheme     string
	Authority  string
	Path       string
	Method     string
	RemoteAddr string
	Headers    map[string][]string
	ClientIP   ClientIPResolution
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

	clientIP, authoritative := pr.ClientIP.value()
	if authoritative {
		ctx = httpsec.ContextWithClientIPOverride(ctx, clientIP)
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
