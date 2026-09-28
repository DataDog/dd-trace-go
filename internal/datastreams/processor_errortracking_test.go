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

func TestBucketExportReportsSerializationErrors(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	sketch := ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor())
	require.NoError(t, sketch.Add(1))
	b := bucket{points: map[uint64]statsGroup{
		1: {
			pathwayLatency: sketch,
			edgeLatency:    sketch,
			payloadSize:    sketch,
		},
	}}

	for failingCall := 1; failingCall <= 3; failingCall++ {
		calls := 0
		b.exportWithMarshaler(TimestampTypeCurrent, nil, func(message proto.Message) ([]byte, error) {
			calls++
			if calls == failingCall {
				return nil, errors.New("serialization failed")
			}
			return proto.Marshal(message)
		})
	}
	client.Flush()

	logs := capture.LogMessages()
	require.Len(t, logs, 3)
	type observedLog struct {
		level      string
		count      uint32
		message    string
		stackTrace string
	}
	messages := make(map[string]observedLog, len(logs))
	for _, entry := range logs {
		message, _, _ := strings.Cut(entry.Message, ": error.error_type=")
		messages[message] = observedLog{
			level:      string(entry.Level),
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
		assert.Equal(t, "ERROR", entry.level)
		assert.Equal(t, uint32(1), entry.count)
		assert.Contains(t, entry.message, "error.error_type=errors.errorString")
		assert.Contains(t, entry.stackTrace, "processor.go")
		assert.Contains(t, entry.stackTrace, "bucket.exportWithMarshaler")
	}
}
