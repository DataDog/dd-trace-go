// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/DataDog/sketches-go/ddsketch"
	sketchpb "github.com/DataDog/sketches-go/ddsketch/pb/sketchpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// sketchGamma is the gamma of the mapping that the Data Streams processor uses for its sketches. A
// decoded value is within (gamma-1)/2, about 0.78%, of the recorded one. The test rounds that up to
// 1% so that a change of the mapping fails with a message that explains the cause.
const (
	sketchGamma     = 1.015625
	sizeRelAccuracy = 0.01
)

// Capture the wire payload: inspecting propagated context cannot verify DSM when injection is off.
type injectionDSMRecorder struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (r *injectionDSMRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/info" {
		// The tracer polls /info every five seconds to learn the capabilities of the agent.
		// Answering it keeps that poll quiet.
		_, _ = io.WriteString(w, `{"endpoints":["/v0.1/pipeline_stats"]}`)
		return
	}
	if req.URL.Path == "/v0.1/pipeline_stats" {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()
	}
	_, _ = io.WriteString(w, "{}")
}

type injectionDSMPoint struct {
	tags          []string
	timestampType string
	size          *ddsketch.DDSketch
}

func (r *injectionDSMRecorder) points(t *testing.T) []injectionDSMPoint {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var points []injectionDSMPoint
	for _, body := range r.bodies {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		require.NoError(t, err)
		decoded, err := msgp.NewReader(zr).ReadIntf()
		require.NoError(t, err)
		require.NoError(t, zr.Close())
		payload, ok := decoded.(map[string]any)
		require.True(t, ok)
		buckets, ok := payload["Stats"].([]any)
		require.True(t, ok)
		for _, rawBucket := range buckets {
			bucket, ok := rawBucket.(map[string]any)
			require.True(t, ok)
			stats, ok := bucket["Stats"].([]any)
			require.True(t, ok)
			for _, rawPoint := range stats {
				point, ok := rawPoint.(map[string]any)
				require.True(t, ok)
				rawTags, ok := point["EdgeTags"].([]any)
				require.True(t, ok)
				tags := make([]string, len(rawTags))
				for i, rawTag := range rawTags {
					tags[i], ok = rawTag.(string)
					require.True(t, ok)
				}
				timestampType, ok := point["TimestampType"].(string)
				require.True(t, ok)
				rawSize, ok := point["PayloadSize"].([]byte)
				require.True(t, ok)
				var pb sketchpb.DDSketch
				require.NoError(t, proto.Unmarshal(rawSize, &pb))
				size, err := ddsketch.FromProto(&pb)
				require.NoError(t, err)
				points = append(points, injectionDSMPoint{tags, timestampType, size})
			}
		}
	}
	return points
}

func TestInjectionSwitchKeepsDataStreamsCheckpoints(t *testing.T) {
	const firstSize = len(`{"message":"hello"}`)
	const secondSize = len(`{"message":"a longer message"}`)
	expectations := map[string]struct {
		tags  []string
		sizes []int
	}{
		"PutRecord":        {[]string{"direction:out", "type:kinesis", "topic:my-stream"}, []int{firstSize + len("pk")}},
		"PutRecords":       {[]string{"direction:out", "type:kinesis", "topic:my-stream"}, []int{firstSize + len("pk1"), secondSize + len("pk2")}},
		"PutEvents":        {[]string{"direction:out", "type:eventbridge", "topic:test", "exchange:my-bus"}, []int{firstSize + len("test") + len("test") + len("my-bus"), secondSize + len("test") + len("test") + len("my-bus")}},
		"SendMessage":      {[]string{"direction:out", "type:sqs", "topic:my-queue"}, []int{firstSize}},
		"SendMessageBatch": {[]string{"direction:out", "type:sqs", "topic:my-queue"}, []int{firstSize, secondSize}},
		"Publish":          {[]string{"direction:out", "type:sns", "topic:my-topic"}, []int{firstSize}},
		"PublishBatch":     {[]string{"direction:out", "type:sns", "topic:my-topic"}, []int{firstSize, secondSize}},
	}
	for _, tc := range injectCases() {
		for _, op := range tc.ops {
			for _, value := range []string{"true", "false"} {
				t.Run(tc.service+"/"+op.operation+"/"+value, func(t *testing.T) {
					clearInjectEnv(t)
					t.Setenv(tc.env, value)
					t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
					t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
					t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
					recorder := new(injectionDSMRecorder)
					agent := httptest.NewServer(recorder)
					t.Cleanup(agent.Close)
					require.NoError(t, tracer.Start(tracer.WithAgentURL(agent.URL), tracer.WithLogStartup(false)))
					t.Cleanup(tracer.Stop)

					injected := op.run(context.Background(), newInjectTestConfig(t))
					require.Len(t, injected, op.entries)
					for i, got := range injected {
						assert.Equal(t, value == "true", got, "entry %d", i)
					}
					// Stop alone may drop queued points; Flush drains the input and exports active buckets.
					tracer.Flush()
					tracer.Stop()

					expected, ok := expectations[op.operation]
					require.True(t, ok, "missing DSM expectations for %s", op.operation)
					// Each checkpoint is exported in both timestamp views. Count samples, not aggregated points.
					points := recorder.points(t)
					require.NotEmpty(t, points)
					for _, point := range points {
						assert.Contains(t, []string{"current", "origin"}, point.timestampType)
						assert.ElementsMatch(t, expected.tags, point.tags)
					}
					for _, kind := range []string{"current", "origin"} {
						var count float64
						var observed []float64
						for _, point := range points {
							if point.timestampType != kind {
								continue
							}
							count += point.size.GetCount()
							// With one or two entries, the endpoints recover each expected sample, even across buckets.
							for i := 0; i < int(point.size.GetCount()); i++ {
								q := float64(i) / float64(max(1, int(point.size.GetCount())-1))
								size, err := point.size.GetValueAtQuantile(q)
								require.NoError(t, err)
								observed = append(observed, size)
							}
						}
						require.Equal(t, float64(op.entries), count, "%s checkpoint count", kind)
						sort.Float64s(observed)
						want := append([]int(nil), expected.sizes...)
						sort.Ints(want)
						require.Len(t, observed, len(want))
						for i := range want {
							assert.InDelta(t, float64(want[i]), observed[i], float64(want[i])*sizeRelAccuracy,
								"%s payload size %d is off by more than the relative accuracy of the Data Streams sketch (gamma %v), "+
									"did the mapping change?", kind, i, sketchGamma)
						}
					}
				})
			}
		}
	}
}
