// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package ddspan

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type TestCase struct{}

func (*TestCase) Setup(context.Context, *testing.T) {}

func (*TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:0/", nil)
	require.NoError(t, err)

	_, _ = spanFromHTTPRequest(req)

	// Verify that a nil pointer whose type implements context.Context does not
	// panic when passed to a //dd:span-annotated function.  Before the nil
	// guard was added the cast to context.Context produced a typed-nil
	// interface, causing tracer.StartSpanFromContext to call Value on a nil
	// pointer and panic.
	spanWithNilNamedCtx(nil)
	spanWithNilOtherCtx(nil)
}

// customCtx is a minimal context.Context implementation that embeds the
// interface.  A nil *customCtx panics on any method call, which is the crash
// path the nil-guard in the //dd:span template must prevent.
type customCtx struct{ context.Context }

//dd:span nil.ctx:named
func spanWithNilNamedCtx(ctx *customCtx) {}

//dd:span nil.ctx:other
func spanWithNilOtherCtx(myCtx *customCtx) {}

//dd:span foo:bar
func spanFromHTTPRequest(*http.Request) (string, error) {
	return tagSpecificSpan()
}
