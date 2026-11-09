// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type benchOTLPRequestBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *benchOTLPRequestBody) Close() error {
	b.closed = true
	return b.closeErr
}

func TestOTLPBenchDiscardTransport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reader   io.Reader
		closeErr error
		wantErr  error
	}{
		{name: "success", reader: strings.NewReader("payload")},
		{name: "read error", reader: iotest.ErrReader(io.ErrUnexpectedEOF), wantErr: io.ErrUnexpectedEOF},
		{name: "close error", reader: strings.NewReader("payload"), closeErr: io.ErrClosedPipe, wantErr: io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &benchOTLPRequestBody{Reader: tc.reader, closeErr: tc.closeErr}
			req, err := http.NewRequest(http.MethodPost, "http://collector.invalid/v1/traces", body)
			require.NoError(t, err)

			resp, err := (benchOTLPDiscardTransport{}).RoundTrip(req)
			assert.True(t, body.closed)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, 0, tc.reader.(*strings.Reader).Len())
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			responseBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Empty(t, responseBody)
			require.NoError(t, resp.Body.Close())
		})
	}

	t.Run("no body", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://collector.invalid/v1/traces", nil)
		require.NoError(t, err)
		resp, err := (benchOTLPDiscardTransport{}).RoundTrip(req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotNil(t, resp.Body)
		require.NoError(t, resp.Body.Close())
	})
}

func TestOTLPBenchFlushFixture(t *testing.T) {
	writer, trace := newBenchOTLPFlushFixture(t)
	recorder := wrapRecordingRoundTripper(writer.transport.client)
	var firstPayload []byte

	for i := range 3 {
		writer.add(trace)
		writer.flush()
		writer.wait()
		require.Len(t, recorder.reqs, i+1)

		req := recorder.reqs[i]
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, otlpContentTypeProto, req.Header.Get("Content-Type"))
		require.NotNil(t, req.GetBody)
		body, err := req.GetBody()
		require.NoError(t, err)
		payload, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		require.NotEmpty(t, payload)

		var data otlptrace.TracesData
		require.NoError(t, proto.Unmarshal(payload, &data))
		require.Len(t, data.ResourceSpans, 1)
		require.Len(t, data.ResourceSpans[0].ScopeSpans, 1)
		require.Len(t, data.ResourceSpans[0].ScopeSpans[0].Spans, 1)
		span := data.ResourceSpans[0].ScopeSpans[0].Spans[0]
		assert.Equal(t, "flush-test", span.Name)
		assert.Equal(t, convertSpanID(1), span.SpanId)
		assert.Equal(t, convertTraceID(0, 2), span.TraceId)
		assert.Equal(t, uint64(1_700_000_000_000_000_000), span.StartTimeUnixNano)
		assert.Equal(t, uint64(1_700_000_000_001_000_000), span.EndTimeUnixNano)
		if i == 0 {
			firstPayload = payload
		} else {
			assert.Equal(t, firstPayload, payload)
		}
	}
}
