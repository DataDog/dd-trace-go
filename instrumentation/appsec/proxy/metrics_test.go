// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

var _ RequestHeaders = (*fakeRequestHeaders)(nil)
var _ ResponseHeaders = (*fakeResponseHeaders)(nil)
var _ HTTPBody = (*fakeBody)(nil)

type fakeRequestHeaders struct {
	eos         bool
	limit       int
	headers     map[string][]string
	ackUntilEOS bool
}

func (f fakeRequestHeaders) GetEndOfStream() bool                                 { return f.eos }
func (f fakeRequestHeaders) MessageType() MessageType                             { return MessageTypeRequestHeaders }
func (f fakeRequestHeaders) SpanOptions(context.Context) []tracer.StartSpanOption { return nil }
func (f fakeRequestHeaders) BodyParsingSizeLimit(context.Context) int             { return f.limit }
func (f fakeRequestHeaders) AckBodyMessagesUntilEndOfStream(context.Context) bool {
	return f.ackUntilEOS
}
func (f fakeRequestHeaders) ExtractRequest(context.Context) (PseudoRequest, error) {
	return PseudoRequest{
		Scheme:     "https",
		Authority:  "datadoghq.com",
		Path:       "/test",
		Method:     "POST",
		Headers:    f.headers,
		RemoteAddr: "127.0.0.1:1234",
	}, nil
}

type fakeResponseHeaders struct {
	eos     bool
	headers map[string][]string
}

func (f fakeResponseHeaders) MessageType() MessageType { return MessageTypeResponseHeaders }
func (f fakeResponseHeaders) GetEndOfStream() bool     { return f.eos }
func (f fakeResponseHeaders) ExtractResponse() (PseudoResponse, error) {
	return PseudoResponse{
		StatusCode: 200,
		Headers:    f.headers,
	}, nil
}

func TestRegisterConfig_OnFirstRequest(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(recorder)()

	mt := mocktracer.Start()
	defer mt.Stop()
	appsec.Start()
	defer appsec.Stop()

	instr := instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
	bodyLimit := 256
	mp := NewProcessor(ProcessorConfig{
		BlockingUnavailable:  true,
		BodyParsingSizeLimit: nil, // use request-provided limit
		Framework:            "test-framework",
		ContinueMessageFunc:  func(_ context.Context, _ ContinueActionOptions) error { return nil },
		BlockMessageFunc:     func(_ context.Context, _ BlockActionOptions) error { return nil },
	}, instr)
	defer mp.Close()

	reqState, err := mp.OnRequestHeaders(context.Background(), fakeRequestHeaders{eos: true, limit: bodyLimit})
	require.NoError(t, err)
	err = mp.OnResponseHeaders(fakeResponseHeaders{eos: true}, &reqState)
	require.ErrorIs(t, err, io.EOF)

	telemetrytest.CheckConfig(t, recorder.Configuration, "appsec.proxy.blockingUnavailable", true)
	telemetrytest.CheckConfig(t, recorder.Configuration, "appsec.proxy.bodyParsingSizeLimit", int64(bodyLimit))
	telemetrytest.CheckConfig(t, recorder.Configuration, "appsec.proxy.framework", "test-framework")
}

type fakeBody struct {
	b   []byte
	eos bool
}

func (f fakeBody) GetEndOfStream() bool     { return f.eos }
func (f fakeBody) MessageType() MessageType { return MessageTypeRequestBody }
func (f fakeBody) GetBody() []byte          { return f.b }

func TestOnBody_SubmitsBodySize_ByDirection(t *testing.T) {
	directions := []string{"request", "response"}
	for _, direction := range directions {
		t.Run(direction, func(t *testing.T) {
			tests := []struct {
				name          string
				limit         int
				body          []byte
				eos           bool
				wantVal       float64
				wantTruncated bool
			}{
				{
					name:          "truncated-larger-than-limit",
					limit:         5,
					body:          []byte("0123456789"),
					eos:           false,
					wantVal:       5.0,
					wantTruncated: true,
				},
				{
					name:          "not-truncated-less-than-limit",
					limit:         15,
					body:          []byte("0123456789"),
					eos:           true,
					wantVal:       10.0,
					wantTruncated: false,
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					recorder := new(telemetrytest.RecordClient)
					defer telemetry.MockClient(recorder)()

					mt := mocktracer.Start()
					defer mt.Stop()
					appsec.Start()
					defer appsec.Stop()

					instr := instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane)
					mp := NewProcessor(ProcessorConfig{
						BlockingUnavailable: false,
						Framework:           "test-framework",
						ContinueMessageFunc: func(_ context.Context, _ ContinueActionOptions) error { return nil },
						BlockMessageFunc:    func(_ context.Context, _ BlockActionOptions) error { return nil },
					}, instr)
					defer mp.Close()

					// ackUntilEOS keeps the stream open past the analyzed chunk so this test can
					// stay focused on the reported body size. Stream lifetime itself is covered by
					// TestOnResponseBodyTruncationStreamLifetime.
					reqHeaders := fakeRequestHeaders{eos: direction != "request", limit: tt.limit, headers: map[string][]string{"Content-Type": {"application/json"}}, ackUntilEOS: true}
					reqState, err := mp.OnRequestHeaders(context.Background(), reqHeaders)
					require.NoError(t, err)

					if direction == "request" {
						err = mp.OnRequestBody(fakeBody{b: tt.body, eos: tt.eos}, &reqState)
						require.NoError(t, err)
						require.Empty(t, reqState.requestBuffer.buffer)
					}

					err = mp.OnResponseHeaders(fakeResponseHeaders{eos: direction != "response", headers: map[string][]string{"Content-Type": {"application/json"}}}, &reqState)

					if direction == "response" {
						require.NoError(t, err)
						err = mp.OnResponseBody(fakeBody{b: tt.body, eos: tt.eos}, &reqState)
						if !tt.eos {
							require.NoError(t, err)
							require.Empty(t, reqState.responseBuffer.buffer)
							err = mp.OnResponseBody(fakeBody{eos: true}, &reqState)
						}
					}
					require.ErrorIs(t, err, io.EOF)

					// Find metric
					found := false
					truncatedStr := fmt.Sprintf("truncated:%v", tt.wantTruncated)
					for key, handle := range recorder.Metrics {
						if key.Namespace == telemetry.NamespaceAppSec && key.Name == "instrum.body_size" &&
							strings.Contains(key.Tags, "direction:"+direction) &&
							strings.Contains(key.Tags, truncatedStr) {
							assert.Equal(t, tt.wantVal, handle.Get())
							found = true
						}
					}
					require.True(t, found, "expected instrum.body_size metric to be recorded with correct tags")
				})
			}
		})
	}
}

// TestOnResponseBodyTruncationStreamLifetime covers the analyzed-before-end-of-stream
// branch under both gateway policies: the analysis is complete once the body overflows the
// parsing limit, so the only difference is whether the stream survives to acknowledge the
// rest of the body.
func TestOnResponseBodyTruncationStreamLifetime(t *testing.T) {
	tests := []struct {
		name            string
		ackUntilEOS     bool
		wantStreamEnded bool
	}{
		{name: "closes-early-when-draining-is-off", ackUntilEOS: false, wantStreamEnded: true},
		{name: "stays-open-when-draining-is-on", ackUntilEOS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			appsec.Start()
			defer appsec.Stop()

			continueCalls := 0
			mp := NewProcessor(ProcessorConfig{
				Framework:           "test-framework",
				ContinueMessageFunc: func(_ context.Context, _ ContinueActionOptions) error { continueCalls++; return nil },
				BlockMessageFunc:    func(_ context.Context, _ BlockActionOptions) error { return nil },
			}, instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane))
			defer mp.Close()

			jsonHeaders := map[string][]string{"Content-Type": {"application/json"}}
			reqState, err := mp.OnRequestHeaders(context.Background(), fakeRequestHeaders{eos: true, limit: 5, headers: jsonHeaders, ackUntilEOS: tt.ackUntilEOS})
			require.NoError(t, err)
			require.NoError(t, mp.OnResponseHeaders(fakeResponseHeaders{eos: false, headers: jsonHeaders}, &reqState))

			// A single chunk overflows the limit, so the analysis completes before EOS.
			continueCalls = 0
			err = mp.OnResponseBody(fakeBody{b: []byte("0123456789"), eos: false}, &reqState)

			require.Equal(t, 1, continueCalls, "the message must be acknowledged either way")
			require.Empty(t, reqState.responseBuffer.buffer, "the analyzed payload must be released")
			if tt.wantStreamEnded {
				require.ErrorIs(t, err, io.EOF, "the stream must end once nothing is left to analyze")
				require.Equal(t, MessageTypeFinished, reqState.State)
				return
			}

			require.NoError(t, err, "the stream must stay open to acknowledge the rest of the body")
			require.True(t, reqState.State.Ongoing())
			require.ErrorIs(t, mp.OnResponseBody(fakeBody{eos: true}, &reqState), io.EOF)
		})
	}
}

func TestRequestStateCloseConcurrentWithLockedFinalize(t *testing.T) {
	var afterHandleCalls atomic.Int64
	insideAfterHandle := make(chan struct{})
	rs := &RequestState{
		Mu:    new(sync.Mutex),
		State: MessageTypeResponseBody,
		afterHandle: func() {
			if afterHandleCalls.Add(1) == 1 {
				close(insideAfterHandle)
				time.Sleep(200 * time.Millisecond)
			}
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rs.Mu.Lock()
		defer rs.Mu.Unlock()
		rs.finalizeResponse()
	}()
	go func() {
		defer wg.Done()
		<-insideAfterHandle
		_ = rs.Close()
	}()
	wg.Wait()

	require.EqualValues(t, 1, afterHandleCalls.Load())
}

func TestOnResponseBodyMisconfigurationAcknowledgesMessage(t *testing.T) {
	// The message is always acknowledged; only the decision to keep the stream open
	// depends on AckBodyMessagesUntilEndOfStream.
	tests := []struct {
		name            string
		endOfStream     bool
		ackUntilEOS     bool
		wantStreamEnded bool
	}{
		{name: "end-of-stream-ends-the-stream", endOfStream: true, wantStreamEnded: true},
		{name: "early-close-when-draining-is-off", endOfStream: false, wantStreamEnded: true},
		{name: "stays-open-when-draining-is-on", endOfStream: false, ackUntilEOS: true},
		{name: "end-of-stream-ends-the-stream-while-draining", endOfStream: true, ackUntilEOS: true, wantStreamEnded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			continueCalls := 0
			mp := NewProcessor(ProcessorConfig{
				ContinueMessageFunc: func(context.Context, ContinueActionOptions) error {
					continueCalls++
					return nil
				},
			}, instrumentation.Load(instrumentation.PackageEnvoyProxyGoControlPlane))
			defer mp.Close()

			state := RequestState{
				Mu:                              new(sync.Mutex),
				Context:                         context.Background(),
				afterHandle:                     func() {},
				State:                           MessageTypeResponseBody,
				ackBodyMessagesUntilEndOfStream: tt.ackUntilEOS,
			}
			err := mp.OnResponseBody(fakeBody{eos: tt.endOfStream}, &state)

			if tt.wantStreamEnded {
				require.ErrorIs(t, err, io.EOF)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, continueCalls)
		})
	}
}
