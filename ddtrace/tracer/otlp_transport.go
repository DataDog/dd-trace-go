// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// otlpTransport sends protobuf-encoded OTLP payloads over HTTP.
// It is the OTLP counterpart to httpTransport (which handles Datadog-protocol traffic).
type otlpTransport struct {
	client        *http.Client
	endpoint      string
	customHeaders map[string]string
}

func newOTLPTransport(client *http.Client, endpoint string, customHeaders map[string]string) *otlpTransport {
	return &otlpTransport{
		client:        client,
		endpoint:      endpoint,
		customHeaders: customHeaders,
	}
}

// send posts a protobuf-encoded payload to the configured OTLP endpoint.
func (t *otlpTransport) send(data []byte) error {
	req, err := http.NewRequest("POST", t.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	for header, value := range t.customHeaders {
		req.Header.Set(header, value)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if code := resp.StatusCode; code >= 400 {
		return fmt.Errorf("HTTP %d: %s", code, http.StatusText(code))
	}
	return nil
}
