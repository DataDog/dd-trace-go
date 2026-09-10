// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestStartSpanFromContext(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	fctx := &fasthttp.RequestCtx{}
	activeSpan := StartSpanFromContext(fctx, "myOp")
	keySpan := fctx.UserValue(instr.ActiveSpanKey())
	assert.Equal(activeSpan, keySpan)
}

// TestStartSpanFromContextChildOfPrecedence pins which parent wins when fctx
// already carries a span and the caller also passes ChildOf.
//
// A span reaches fctx through SetUserValue, so the value is readable as
// internal.ActiveSpanKey but carries none of the snapshot that
// tracer.ContextWithSpan leaves behind. That routes it through the inferred-parent
// branch of tracer.StartSpanFromContext, where an explicit ChildOf wins. Nothing
// asserted this before, which is how the doc comment on StartSpanFromContext went
// stale when that precedence changed.
func TestStartSpanFromContextChildOfPrecedence(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// parentOf finishes the child so the mock tracer records it, then reports the
	// parent it actually resolved. Each case builds its own fctx: this helper
	// overwrites the user value, so a shared fctx would make the second case
	// parent to the first case's child rather than to its own ambient span.
	parentOf := func(t *testing.T, opts ...tracer.StartSpanOption) (parentID, ambientID uint64) {
		t.Helper()
		mt.Reset()

		fctx := &fasthttp.RequestCtx{}
		ambient := StartSpanFromContext(fctx, "ambient")
		child := StartSpanFromContext(fctx, "child", opts...)
		child.Finish()

		for _, s := range mt.FinishedSpans() {
			if s.OperationName() == "child" {
				return s.ParentID(), ambient.Context().SpanID()
			}
		}
		t.Fatal(`no finished span named "child"`)
		return 0, 0
	}

	t.Run("explicit ChildOf wins", func(t *testing.T) {
		explicit := tracer.StartSpan("explicit")
		wantParent := explicit.Context().SpanID()

		parentID, ambientID := parentOf(t, tracer.ChildOf(explicit.Context()))

		assert.Equal(t, wantParent, parentID,
			"an explicit ChildOf must outrank the span already in fctx")
		assert.NotEqual(t, ambientID, parentID)
	})

	t.Run("fctx span parents when no ChildOf is passed", func(t *testing.T) {
		parentID, ambientID := parentOf(t)

		assert.Equal(t, ambientID, parentID,
			"without ChildOf the span in fctx still parents")
	})
}
