// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceSource(t *testing.T) {
	t.Run("ManualSpanWithServiceName", func(t *testing.T) {
		// A manually created span with an explicit ServiceName gets _dd.srv_src = "m".
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op", ServiceName("my-service"))
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		assert.Equal(t, "m", span.meta[keyServiceSource])
	})

	t.Run("ManualSpanSetTagOverride", func(t *testing.T) {
		// When SetTag(ext.ServiceName, ...) is called after span creation,
		// the source is overridden to "m".
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op")
		span.SetTag(ext.ServiceName, "override")
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		assert.Equal(t, "m", span.meta[keyServiceSource])
	})

	t.Run("ChildInheritsSrvSrcFromParent", func(t *testing.T) {
		// A child span inherits _dd.srv_src from its parent.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		parent := tracer.StartSpan("parent", ServiceName("my-service"))
		child := tracer.StartSpan("child", ChildOf(parent.Context()))
		child.Finish()
		parent.Finish()

		child.mu.RLock()
		defer child.mu.RUnlock()
		assert.Equal(t, "m", child.meta[keyServiceSource])
	})

	t.Run("NoExplicitServiceNoSrvSrc", func(t *testing.T) {
		// A span with no explicit service name should not have _dd.srv_src.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op")
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		_, hasSrvSrc := span.meta[keyServiceSource]
		assert.False(t, hasSrvSrc, "_dd.srv_src should not be set when no service is explicitly set")
	})

	t.Run("ChildWithExplicitServiceGetsSrvSrc", func(t *testing.T) {
		// A child span that explicitly sets its own service name gets _dd.srv_src = "m".
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		parent := tracer.StartSpan("parent", ServiceName("parent-service"))
		child := tracer.StartSpan("child", ChildOf(parent.Context()), ServiceName("child-service"))
		child.Finish()
		parent.Finish()

		child.mu.RLock()
		defer child.mu.RUnlock()
		assert.Equal(t, "m", child.meta[keyServiceSource])
	})

	t.Run("ServiceMappingSetsSrvSrc", func(t *testing.T) {
		// When a service mapping renames a span's service, _dd.srv_src = "opt.mapping".
		tracer, err := newTracer(WithServiceMapping("original", "remapped"))
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op", ServiceName("original"))
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		assert.Equal(t, "remapped", span.service)
		assert.Equal(t, "opt.mapping", span.meta[keyServiceSource])
	})
}
