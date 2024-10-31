// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"errors"
	"fmt"
	"io"
)

const (
	// coverageSubDomain is the subdomain for the coverage endpoint.
	coverageSubDomain string = "citestcov-intake"
	// coverageURLPath is the URL path for the coverage endpoint.
	coverageURLPath string = "api/v2/citestcov"
)

// NewClientForCodeCoverage creates a new client for sending code coverage payloads.
func NewClientForCodeCoverage() Client {
	return NewClientWithServiceNameAndSubdomain("", coverageSubDomain)
}

// SendCoveragePayload sends a code coverage payload to the backend.
func (c *client) SendCoveragePayload(ciTestCovPayload io.Reader) error {
	if ciTestCovPayload == nil {
		return errors.New("coverage payload is nil")
	}

	// Create a dummy event to send with the coverage payload.
	dummyEvent := FormFile{
		FieldName:   "event",
		ContentType: ContentTypeJSON,
		FileName:    "fileevent.json",
		Content:     []byte("{\"dummy\": true}"),
	}

	// Send the coverage payload.
	request := RequestConfig{
		Method:  "POST",
		URL:     c.getURLPath(coverageURLPath),
		Headers: c.headers,
		Files: []FormFile{
			dummyEvent,
			{
				FieldName:   "coveragex",
				Content:     ciTestCovPayload,
				FileName:    "filecoveragex.msgpack",
				ContentType: ContentTypeMessagePack,
			},
		},
	}

	response, responseErr := c.handler.SendRequest(request)
	if responseErr != nil {
		return fmt.Errorf("failed to send packfile request: %s", responseErr.Error())
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("unexpected response code %d: %s", response.StatusCode, string(response.Body))
	}

	return nil
}
