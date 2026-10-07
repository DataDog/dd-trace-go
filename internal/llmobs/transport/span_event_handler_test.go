// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPushSpanEventsHandler(t *testing.T) {
	for _, mode := range []string{"success", "retry", "encoding-error"} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan []byte, 4)
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- body
				if attempts.Add(1) == 1 && mode == "retry" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			t.Cleanup(server.Close)
			transport := newTestTransport(t, server.URL)
			var copies [][]byte
			panicCalls := 0
			events := []*LLMObsSpanEvent{
				{SpanID: "panic", SpanEventHandler: func(event []byte) {
					panicCalls++
					event[0] = '!'
					panic("private-content")
				}},
				{SpanID: "copy", Name: "<&>", StartNS: 1791323456123456789,
					SpanEventHandler: func(event []byte) { copies = append(copies, event) }},
				{SpanID: "primary-only"},
			}
			if mode == "encoding-error" {
				events[1].Metrics = map[string]float64{"invalid": math.NaN()}
			}
			result, err := transport.PushSpanEventsWithResult(t.Context(), events)
			if mode == "encoding-error" {
				require.ErrorContains(t, err, "failed to json encode body")
				require.Zero(t, attempts.Load())
				require.Zero(t, panicCalls)
				require.Empty(t, copies)
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, result.StatusCode)
			require.Equal(t, 1, panicCalls)
			require.Len(t, copies, 1)
			body := <-requests
			if mode == "retry" {
				require.Equal(t, 2, result.Attempts)
				require.Equal(t, body, <-requests)
			} else {
				require.Equal(t, 1, result.Attempts)
			}
			var envelopes []struct {
				Spans []json.RawMessage `json:"spans"`
			}
			require.NoError(t, json.Unmarshal(body, &envelopes))
			require.Len(t, envelopes, 3)
			require.Equal(t, []byte(envelopes[1].Spans[0]), copies[0])
			require.Contains(t, string(copies[0]), "<&>")
			require.Contains(t, string(copies[0]), "1791323456123456789")
		})
	}
}
