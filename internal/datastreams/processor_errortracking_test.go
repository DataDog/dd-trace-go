// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package datastreams

import (
	"errors"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestBucketExportReportsSerializationErrorsOncePerFlush(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	const points = 32
	sketch := ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor())
	require.NoError(t, sketch.Add(1))
	b := bucket{points: make(map[uint64]statsGroup, points)}
	for i := range points {
		b.points[uint64(i)] = statsGroup{
			pathwayLatency: sketch,
			edgeLatency:    sketch,
			payloadSize:    sketch,
		}
	}

	for _, phase := range []int{1, 2, 3} {
		calls := 0
		exported := b.exportWithMarshaler(TimestampTypeCurrent, nil, func(message proto.Message) ([]byte, error) {
			calls++
			if calls%phase == 0 {
				return nil, errors.New("sensitive serialization failure")
			}
			return proto.Marshal(message)
		})
		assert.Empty(t, exported.Stats, "every point is dropped when serialization phase %d fails", phase)
	}

	valid := b.export(TimestampTypeCurrent, nil)
	require.Len(t, valid.Stats, points, "successful serialization must preserve all points")

	client.Flush()
	logs := capture.LogMessages()
	require.Len(t, logs, 3)

	messages := make(map[string]struct {
		count      uint32
		message    string
		stackTrace string
	}, len(logs))
	for _, entry := range logs {
		message, _, _ := strings.Cut(entry.Message, ": error.error_type=")
		messages[message] = struct {
			count      uint32
			message    string
			stackTrace string
		}{
			count:      entry.Count,
			message:    entry.Message,
			stackTrace: entry.StackTrace,
		}
	}
	for _, message := range []string{
		"can't serialize pathway latency. Ignoring",
		"can't serialize edge latency. Ignoring",
		"can't serialize payload size. Ignoring",
	} {
		entry, ok := messages[message]
		require.True(t, ok, "missing telemetry report for %q", message)
		assert.Equal(t, uint32(1), entry.count, "one report must cover every failed point in the flush")
		assert.Equal(t, message+": error.error_type=errors.errorString", entry.message)
		assert.NotContains(t, entry.message, "sensitive serialization failure")
		require.NotEmpty(t, entry.stackTrace)
		assert.Contains(t, entry.stackTrace, "processor.go")
		assert.Contains(t, entry.stackTrace, "bucket.exportWithMarshaler")
	}
}
