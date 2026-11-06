// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const userService = "aws-user-service"

// counted starts the container before the harness starts the tracer, so the
// Docker API calls it makes are not counted and a test case can pin the exact
// span count.
type counted struct {
	base
}

func (c *counted) PreBootstrap(ctx context.Context, t *testing.T) {
	c.setup(ctx, t)
}

// runUnderRoot runs the call under a test.root span, so the expected traces
// can require the AWS span to be its direct child. A second AWS span would sit
// between the two.
func (b *base) runUnderRoot(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	b.run(ctx, t)
}

// expectedRootTraces expects one AWS span under test.root, reported with
// service.
func (b *base) expectedRootTraces(service string) trace.Traces {
	traces := b.expectedTraces()
	for _, tr := range traces {
		tr.Tags["service"] = service
		for _, child := range tr.Children {
			delete(child.Tags, "service")
		}
	}
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: traces,
		},
	}
}
