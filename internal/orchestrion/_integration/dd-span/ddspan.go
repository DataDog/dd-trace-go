// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package ddspan

import (
	"context"
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/require"
)

type TestCase struct{}

func (*TestCase) Setup(context.Context, *testing.T) {}

func (*TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:0/", nil)
	require.NoError(t, err)

	_, _ = spanFromHTTPRequest(req)
}

//dd:span foo:bar
func spanFromHTTPRequest(*http.Request) (string, error) {
	return tagSpecificSpan()
}
