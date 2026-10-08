// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

// Package opensearchapi provides tracing functions for tracing the opensearch-project/opensearch-go/v4/opensearchapi package (https://github.com/opensearch-project/opensearch-go).
package opensearchapi

import (
	opensearchtrace "github.com/DataDog/dd-trace-go/contrib/opensearch-project/opensearch-go.v4/v2"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var _ = instrumentation.Load(instrumentation.PackageOpenSearchProjectOpenSearchGoV4)

// NewClient returns a opensearchapi client enhanced with tracing.
func NewClient(config opensearchapi.Config, opts ...opensearchtrace.Option) (*opensearchapi.Client, error) {
	c, err := opensearchapi.NewClient(config)
	if err != nil {
		return nil, err
	}
	// Startup discovery may already be reading c.Client.Transport in another goroutine.
	// Trace a copy so that goroutine keeps using the upstream client unchanged.
	traced := *c.Client
	opensearchtrace.TraceClient(&traced, opts...)
	c.Client = &traced
	return c, nil
}
