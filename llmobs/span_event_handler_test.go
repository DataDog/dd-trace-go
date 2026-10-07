// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

func TestExportSpanEventHasNoCallback(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeFor[export.SpanEvent]())
	for _, field := range fields {
		t.Run(field.Name, func(t *testing.T) {
			require.NotEqual(t, reflect.Func, field.Type.Kind(), "export.SpanEvent must not expose a live-span callback")
		})
	}
}

func TestWithSpanEventHandlerScope(t *testing.T) {
	for _, scope := range []string{"inherited", "cleared", "replaced"} {
		t.Run(scope, func(t *testing.T) {
			collector := testTracer(t)
			parentEvents, childEvents := make(chan []byte, 2), make(chan []byte, 1)
			ctx := llmobs.WithSpanEventHandler(t.Context(), func(event []byte) { parentEvents <- event })
			parent, ctx := llmobs.StartWorkflowSpan(ctx, "parent")
			switch scope {
			case "cleared":
				ctx = llmobs.WithSpanEventHandler(ctx, nil)
			case "replaced":
				ctx = llmobs.WithSpanEventHandler(ctx, func(event []byte) { childEvents <- event })
			}
			child, _ := llmobs.StartToolSpan(ctx, "child")
			child.Finish()
			child.Finish()
			parent.Finish()
			tracer.Stop()
			close(parentEvents)
			close(childEvents)
			require.Equal(t, child.SpanID(), collector.RequireSpan(t, "child").SpanID)
			require.Equal(t, parent.SpanID(), collector.RequireSpan(t, "parent").SpanID)

			for _, destination := range []struct {
				events <-chan []byte
				want   []string
			}{
				{parentEvents, map[string][]string{
					"inherited": {parent.SpanID(), child.SpanID()},
					"cleared":   {parent.SpanID()},
					"replaced":  {parent.SpanID()},
				}[scope]},
				{childEvents, map[string][]string{"replaced": {child.SpanID()}}[scope]},
			} {
				var ids []string
				for event := range destination.events {
					var identity struct {
						SpanID string `json:"span_id"`
					}
					require.NoError(t, json.Unmarshal(event, &identity))
					ids = append(ids, identity.SpanID)
				}
				require.ElementsMatch(t, destination.want, ids)
			}
		})
	}
}
