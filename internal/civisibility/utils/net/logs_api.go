// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"fmt"
	"io"
)

const (
	// logsSubDomain is the subdomain for the logs endpoint.
	logsSubDomain string = "http-intake.logs"
	// logsURLPath is the URL path for the logs endpoint.
	logsURLPath string = "api/v2/logs"
)

// NewClientForLogs creates a new client for sending logs payloads.
func NewClientForLogs() Client {
	return NewClientWithServiceNameAndSubdomain("", logsSubDomain)
}

// SendLogs sends a logs payload to the backend.
func (c *client) SendLogs(logsPayload io.Reader) error {

	// Send the coverage payload.
	request := RequestConfig{
		Method:     "POST",
		URL:        c.getURLPath(logsURLPath),
		Headers:    c.headers,
		Body:       logsPayload,
		Format:     FormatJSON,
		Compressed: true,
	}

	response, responseErr := c.handler.SendRequest(request)

	if responseErr != nil {
		return fmt.Errorf("failed to send logs: %s", responseErr)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("unexpected response code %d: %s", response.StatusCode, string(response.Body))
	}

	return nil
}
