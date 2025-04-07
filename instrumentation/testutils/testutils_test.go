// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package testutils_test

import (
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/require"
)

func TestSetPropagatingTag(t *testing.T) {
	t.Run("propagating tag is set", func(t *testing.T) {
		tracer.Start()
		defer tracer.Stop()

		span := tracer.StartSpan("test")
		defer span.Finish()

		ctx := span.Context()
		testutils.SetPropagatingTag(t, ctx, "test", "test")

		dst := map[string]string{}
		carrier := tracer.TextMapCarrier(dst)
		tracer.Inject(ctx, &carrier)

		propagatingTags := strings.Split(dst["x-datadog-tags"], ",")
		require.NotEmpty(t, propagatingTags)
		require.Contains(t, propagatingTags, "test=test")
	})
	t.Run("panics", func(t *testing.T) {
		require.Panics(t, func() {
			ctx := tracer.SpanContext{}
			// No trace attached to the context, it will panic
			testutils.SetPropagatingTag(t, &ctx, "test", "test")
		})
	})
}
