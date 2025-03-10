// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

// Package opensearchapi provides tracing functions for tracing the opensearch-project/opensearch-go/v4/opensearchapi package (https://github.com/opensearch-project/opensearch-go).
package opensearchapi

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	opensearchtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/opensearch-project/opensearch-go.v4"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func init() {
	telemetry.LoadIntegration("opensearch-project/opensearch-go/v4")
	tracer.MarkIntegrationImported("github.com/opensearch-project/opensearch-go/v4")
}

// NewClient returns a opensearchapi client enhanced with tracing.
func NewClient(config opensearchapi.Config, opts ...opensearchtrace.Option) (*opensearchapi.Client, error) {
	if config.Client.Transport == nil {
		config.Client.Transport = opensearchtrace.WrapRoundTripper(http.DefaultTransport)
	} else {
		config.Client.Transport = opensearchtrace.WrapRoundTripper(config.Client.Transport)
	}
	c, err := opensearchapi.NewClient(config)
	if err != nil {
		return nil, err
	}
	opensearchtrace.WrapClient(c.Client, opts...)
	return c, nil
}
