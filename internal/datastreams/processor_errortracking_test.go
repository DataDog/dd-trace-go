// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package datastreams

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func serializationTestBucket(t *testing.T, start uint64) bucket {
	t.Helper()

	sketch := ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor())
	require.NoError(t, sketch.Add(1))
	return bucket{
		points: map[uint64]statsGroup{0: {
			pathwayLatency: sketch,
			edgeLatency:    sketch,
			payloadSize:    sketch,
		}},
		start:    start,
		duration: uint64(bucketDuration),
	}
}

func TestProcessorFlushReportsSerializationErrorsOnceAcrossBuckets(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	p := &Processor{
		tsTypeCurrentBuckets: map[bucketKey]bucket{
			{serviceName: "service", btime: 0}: serializationTestBucket(t, 0),
			{serviceName: "service", btime: 1}: serializationTestBucket(t, 1),
		},
		tsTypeOriginBuckets: map[bucketKey]bucket{
			{serviceName: "service", btime: 2}: serializationTestBucket(t, 2),
			{serviceName: "service", btime: 3}: serializationTestBucket(t, 3),
		},
	}

	calls := 0
	payloads := p.flushWithMarshaler(time.Unix(20, 0), func(message proto.Message) ([]byte, error) {
		calls++
		if calls == 1 || calls == 3 || calls == 6 {
			return nil, errors.New("sensitive serialization failure")
		}
		return proto.Marshal(message)
	})

	assert.Equal(t, 9, calls)
	assert.Empty(t, p.tsTypeCurrentBuckets)
	assert.Empty(t, p.tsTypeOriginBuckets)
	require.Contains(t, payloads, "service")
	require.Len(t, payloads["service"].Stats, 4)
	var exportedPoints int
	for _, bucket := range payloads["service"].Stats {
		exportedPoints += len(bucket.Stats)
	}
	assert.Equal(t, 1, exportedPoints, "the fully serialized bucket must remain exportable")

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
		assert.Equal(t, uint32(1), entry.count, "one report must cover the processor flush")
		assert.Equal(t, message+": error.error_type=errors.errorString", entry.message)
		assert.NotContains(t, entry.message, "sensitive serialization failure")
		require.NotEmpty(t, entry.stackTrace)
		assert.Contains(t, entry.stackTrace, "processor.go")
		assert.Contains(t, entry.stackTrace, "flushWithMarshaler")
	}
}

func TestProcessorFlushDoesNotReportSerializationErrorsWithoutFailures(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	p := &Processor{
		tsTypeCurrentBuckets: make(map[bucketKey]bucket),
		tsTypeOriginBuckets:  make(map[bucketKey]bucket),
	}
	assert.Empty(t, p.flush(time.Unix(20, 0)), "an empty flush must not report")

	p.tsTypeCurrentBuckets[bucketKey{serviceName: "service", btime: 0}] = serializationTestBucket(t, 0)
	payloads := p.flush(time.Unix(20, 0))
	require.Len(t, payloads["service"].Stats, 1)
	require.Len(t, payloads["service"].Stats[0].Stats, 1)

	client.Flush()
	assert.Empty(t, capture.LogMessages(), "a successful flush must not report")
}
