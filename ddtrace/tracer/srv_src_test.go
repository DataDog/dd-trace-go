// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	sharedinternal "github.com/DataDog/dd-trace-go/v2/internal"
)

func BenchmarkServiceOverrideTag(b *testing.B) {
	tracer, err := newTracer()
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	b.Run("KeyServiceSource_ServiceOverride", func(b *testing.B) {
		for b.Loop() {
			span := tracer.StartSpan("test.op", Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "my-service", Source: serviceSourceManual}))
			span.Finish()
		}
	})

	b.Run("ServiceName_Tag", func(b *testing.B) {
		for b.Loop() {
			span := tracer.StartSpan("test.op", Tag(ext.ServiceName, "my-service"))
			span.Finish()
		}
	})
}

func TestServiceSource(t *testing.T) {
	t.Run("SetTagServiceName", func(t *testing.T) {
		// Setting ext.ServiceName via SetTag should set _dd.svc_src to serviceSourceManual.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op")
		span.SetTag(ext.ServiceName, "custom-service")
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		kss, _ := span.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, "custom-service", span.service)
		assert.Equal(t, serviceSourceManual, kss)
	})

	t.Run("ServiceOverrideTag", func(t *testing.T) {
		// Setting ext.KeyServiceSource with a ServiceOverride should set both service and serviceSource.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op", Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "my-service", Source: serviceSourceManual}))
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		kss, _ := span.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, "my-service", span.service)
		assert.Equal(t, serviceSourceManual, kss)
	})

	t.Run("ChildInheritsSrvSrcFromParent", func(t *testing.T) {
		// A child span inherits _dd.svc_src from its parent.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		parent := tracer.StartSpan("parent", Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "my-service", Source: serviceSourceManual}))
		child := tracer.StartSpan("child", ChildOf(parent.Context()))
		child.Finish()
		parent.Finish()

		child.mu.RLock()
		defer child.mu.RUnlock()
		v, _ := child.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, "m", v)
	})

	t.Run("NoExplicitServiceNoSrvSrc", func(t *testing.T) {
		// A span with no explicit service name should not have _dd.svc_src.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op")
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		_, hasSrvSrc := span.meta.Get(ext.KeyServiceSource)
		assert.False(t, hasSrvSrc, "_dd.svc_src should not be set when no service is explicitly set")
	})

	t.Run("ChildWithExplicitServiceGetsSrvSrc", func(t *testing.T) {
		// A child span that explicitly sets its own service name gets _dd.svc_src = "m".
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		parent := tracer.StartSpan("parent", Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "parent-service", Source: "m"}))
		child := tracer.StartSpan("child", ChildOf(parent.Context()), Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "child-service", Source: "m"}))
		child.Finish()
		parent.Finish()

		child.mu.RLock()
		defer child.mu.RUnlock()
		v, _ := child.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, "m", v)
	})

	t.Run("ServiceMappingSetsSrvSrc", func(t *testing.T) {
		// When a service mapping renames a span's service, _dd.svc_src = "opt.mapping".
		tracer, err := newTracer(WithServiceMapping("original", "remapped"))
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op", Tag(ext.KeyServiceSource, sharedinternal.ServiceOverride{Name: "original", Source: serviceSourceManual}))
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		assert.Equal(t, "remapped", span.service)
		v, _ := span.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, ext.ServiceSourceMapping, v)
	})

	t.Run("SetMetaInitServiceName", func(t *testing.T) {
		// Setting ext.ServiceName via setMetaInit (e.g. from global tags) should set serviceSourceManual.
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		span := tracer.StartSpan("test.op")
		span.mu.Lock()
		span.setMetaInit(ext.ServiceName, "from-meta-init")
		span.mu.Unlock()
		span.Finish()

		span.mu.RLock()
		defer span.mu.RUnlock()
		kss, _ := span.meta.Get(ext.KeyServiceSource)
		assert.Equal(t, "from-meta-init", span.service)
		assert.Equal(t, serviceSourceManual, kss)
	})
}
