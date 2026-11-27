// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package mongo

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// TestMaxQuerySizeUTF8 is a regression test: a query cut by WithMaxQuerySize must stay valid
// UTF-8, dropping a multi-byte character the limit would split.
func TestMaxQuerySizeUTF8(t *testing.T) {
	cmd, err := bson.Marshal(bson.D{{Key: "find", Value: "café€😀"}})
	require.NoError(t, err)
	query := `{"find":"café€😀"}`

	// queryTag runs one command through a monitor built with opts and returns the query tag.
	queryTag := func(t *testing.T, opts ...Option) (string, bool) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mon := NewMonitor(opts...)
		mon.Started(context.Background(), &event.CommandStartedEvent{
			Command: bson.Raw(cmd), CommandName: "find", DatabaseName: "db", ConnectionID: "c", RequestID: 1,
		})
		mon.Succeeded(context.Background(), &event.CommandSucceededEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", ConnectionID: "c", RequestID: 1},
		})
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		v, ok := spans[0].Tag("mongodb.query").(string)
		return v, ok
	}

	t.Run("not truncated by default", func(t *testing.T) {
		got, ok := queryTag(t)
		require.True(t, ok)
		assert.Equal(t, query, got)
	})

	t.Run("zero omits the tag", func(t *testing.T) {
		_, ok := queryTag(t, WithMaxQuerySize(0))
		assert.False(t, ok)
	})

	t.Run("cut inside a rune", func(t *testing.T) {
		// Two bytes into the 4-byte emoji.
		got, _ := queryTag(t, WithMaxQuerySize(strings.Index(query, "😀")+2))
		assert.Equal(t, `{"find":"café€`, got)
	})

	for limit := 1; limit <= len(query)+1; limit++ {
		t.Run("limit "+strconv.Itoa(limit), func(t *testing.T) {
			got, ok := queryTag(t, WithMaxQuerySize(limit))
			require.True(t, ok)
			assert.True(t, utf8.ValidString(got), "%q", got)
			assert.True(t, strings.HasPrefix(query, got), "%q", got)
			assert.LessOrEqual(t, len(got), limit)
			assert.Greater(t, len(got), min(limit, len(query))-utf8.UTFMax)
		})
	}
}
