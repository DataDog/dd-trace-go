// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package apimcallout

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
)

var (
	_ proxy.RequestHeaders  = (*messageRequestHeaders)(nil)
	_ proxy.ResponseHeaders = (*messageResponseHeaders)(nil)
	_ proxy.HTTPBody        = (*messageBody)(nil)
)

// calloutMessage represents the JSON body sent by the gateway on POST /.
// The Addresses field is phase-dependent and decoded separately.
type calloutMessage struct {
	Addresses json.RawMessage `json:"addresses"`
	Gateway   string          `json:"gateway,omitempty"`
	RequestID string          `json:"request-id,omitempty"`
	Phase     string          `json:"phase,omitempty"`
}

// calloutResult represents the JSON response returned to the gateway.
type calloutResult struct {
	RequestID        string              `json:"request-id,omitempty"`
	PropagateHeaders map[string][]string `json:"propagate-headers,omitempty"`
	AllowedBodySize  *int                `json:"allowed-body-size,omitempty"`
	Block            *blockResult        `json:"block,omitempty"`
}

// blockResult represents a blocking decision sent back to the gateway.
type blockResult struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Content string              `json:"content,omitempty"`
}

// addressesRequestHeaders holds the phase-dependent addresses for the request headers phase.
type addressesRequestHeaders struct {
	Method     string              `json:"method"`
	Scheme     string              `json:"scheme"`
	Authority  string              `json:"authority"`
	Path       string              `json:"path"`
	RemoteAddr string              `json:"remote_addr"`
	Headers    map[string][]string `json:"headers"`
	Body       json.RawMessage     `json:"body,omitempty"`
}

// addressesResponseHeaders holds the phase-dependent addresses for the response headers phase.
type addressesResponseHeaders struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       json.RawMessage     `json:"body,omitempty"`
}

// addressesBody holds the phase-dependent addresses for the body phase.
type addressesBody struct {
	Body json.RawMessage `json:"body"`
}

// messageRequestHeaders implements proxy.RequestHeaders for the callout request.
type messageRequestHeaders struct {
	addr                 *addressesRequestHeaders
	gateway              string
	bodyParsingSizeLimit *int
}

func (m *messageRequestHeaders) ExtractRequest(_ context.Context) (proxy.PseudoRequest, error) {
	if m.addr.Method == "" || m.addr.Path == "" {
		return proxy.PseudoRequest{}, errors.New("missing required fields: method and path are required")
	}

	headers := m.addr.Headers
	if headers == nil {
		headers = make(map[string][]string)
	}

	// Normalize header keys to canonical form
	normalized := make(http.Header, len(headers))
	for k, v := range headers {
		normalized[http.CanonicalHeaderKey(k)] = v
	}

	if m.addr.Authority != "" {
		if _, ok := normalized["Host"]; !ok {
			normalized["Host"] = []string{m.addr.Authority}
		}
	}

	scheme := m.addr.Scheme
	if scheme == "" {
		scheme = "https"
	}

	return proxy.PseudoRequest{
		Method:     m.addr.Method,
		Authority:  m.addr.Authority,
		Path:       m.addr.Path,
		Scheme:     scheme,
		Headers:    normalized,
		RemoteAddr: m.addr.RemoteAddr,
	}, nil
}

func (m *messageRequestHeaders) GetEndOfStream() bool {
	// When a body parsing size limit is configured and positive, always report
	// that the stream is not ended so the processor's content-type check
	// becomes the sole gatekeeper for body processing.
	if m.bodyParsingSizeLimit != nil && *m.bodyParsingSizeLimit > 0 {
		return false
	}
	return true
}

func (m *messageRequestHeaders) MessageType() proxy.MessageType {
	return proxy.MessageTypeRequestHeaders
}

func (m *messageRequestHeaders) SpanOptions(_ context.Context) []tracer.StartSpanOption {
	component := "apim-callout"
	if m.gateway == "boomi" {
		component = "boomi-callout"
	}
	return []tracer.StartSpanOption{tracer.Tag(ext.Component, component)}
}

func (m *messageRequestHeaders) BodyParsingSizeLimit(_ context.Context) int {
	if m.bodyParsingSizeLimit != nil {
		return *m.bodyParsingSizeLimit
	}
	return proxy.DefaultBodyParsingSizeLimit
}

// messageResponseHeaders implements proxy.ResponseHeaders for the callout response.
type messageResponseHeaders struct {
	addr                 *addressesResponseHeaders
	bodyParsingSizeLimit *int
}

func (m *messageResponseHeaders) ExtractResponse() (proxy.PseudoResponse, error) {
	headers := m.addr.Headers
	if headers == nil {
		headers = make(map[string][]string)
	}

	normalized := make(http.Header, len(headers))
	for k, v := range headers {
		normalized[http.CanonicalHeaderKey(k)] = v
	}

	return proxy.PseudoResponse{
		StatusCode: m.addr.StatusCode,
		Headers:    normalized,
	}, nil
}

func (m *messageResponseHeaders) GetEndOfStream() bool {
	// Same strategy as messageRequestHeaders: let the processor's content-type
	// check be the sole gatekeeper when body parsing is configured.
	if m.bodyParsingSizeLimit != nil && *m.bodyParsingSizeLimit > 0 {
		return false
	}
	return true
}

func (m *messageResponseHeaders) MessageType() proxy.MessageType {
	return proxy.MessageTypeResponseHeaders
}

// messageBody implements proxy.HTTPBody for base64-encoded bodies in the callout.
type messageBody struct {
	body []byte
	m    proxy.MessageType
}

func (m *messageBody) GetEndOfStream() bool {
	return true // callout bodies are always complete (not streamed)
}

func (m *messageBody) GetBody() []byte {
	return m.body
}

func (m *messageBody) MessageType() proxy.MessageType {
	return m.m
}

// hasRawBody reports whether the raw JSON body field contains a non-empty JSON string.
// A json.RawMessage for an empty string is `""` (len 2), so we need len > 2.
func hasRawBody(raw json.RawMessage) bool {
	first := bytes.IndexByte(raw, '"')
	if first < 0 {
		return false
	}
	last := bytes.LastIndexByte(raw, '"')
	return last > first+1
}

// decodeRawBase64Body extracts the JSON string from a json.RawMessage and
// base64-decodes its content in place without intermediate copies.
// Returns nil if the raw message is empty/null.
func decodeRawBase64Body(raw json.RawMessage) ([]byte, error) {
	first := bytes.IndexByte(raw, '"')
	if first < 0 {
		return nil, nil
	}
	last := bytes.LastIndexByte(raw, '"')
	if last <= first+1 {
		return nil, nil
	}
	src := raw[first+1 : last]
	dst := make([]byte, base64.StdEncoding.DecodedLen(len(src)))
	n, err := base64.StdEncoding.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}
