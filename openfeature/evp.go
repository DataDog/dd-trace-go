// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type evpClient struct {
	httpClient *http.Client
	agentURL   *url.URL
	jsonConfig jsoniter.API
}

func newEVPClient() *evpClient {
	agentURL := internal.AgentURLFromEnv()
	var httpClient *http.Client
	if agentURL.Scheme == "unix" {
		httpClient = internal.UDSClient(agentURL.Path, defaultHTTPTimeout)
		agentURL = internal.UnixDataSocketURL(agentURL.Path)
	} else {
		httpClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	}

	return &evpClient{
		httpClient: httpClient,
		agentURL:   agentURL,
		jsonConfig: jsoniter.Config{}.Froze(),
	}
}

func (c *evpClient) post(endpoint, eventName string, payload any) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}

	payloadBytes, err := c.marshalPayload(eventName, payload)
	if err != nil {
		return err
	}
	return c.postBytes(endpoint, eventName, payloadBytes)
}

func (c *evpClient) marshalPayload(eventName string, payload any) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("EVP client is not configured")
	}

	payloadBytes, err := c.jsonConfig.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode %s payload: %w", eventName, err)
	}
	return payloadBytes, nil
}

func (c *evpClient) postBytes(endpoint, eventName string, payloadBytes []byte) error {
	if c == nil {
		return fmt.Errorf("EVP client is not configured")
	}

	u := *c.agentURL
	u.Path = endpoint
	requestURL := u.String()

	req, err := http.NewRequestWithContext(context.Background(), "POST", requestURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(evpSubdomainHeader, evpSubdomainValue)

	log.Debug("openfeature: sending %s events to %s", eventName, requestURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
