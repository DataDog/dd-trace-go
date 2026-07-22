// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package instrumentation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type capturedMetaStructSpan struct {
	meta       map[string]any
	metaStruct map[string]any
}

type capturedMetaStructResult struct {
	span capturedMetaStructSpan
	err  error
}

func TestSetMetaStructTag(t *testing.T) {
	fallbackErr := errors.New("fallback error")
	tests := []struct {
		name        string
		supported   bool
		fallbackErr error
	}{
		{name: "fallback", supported: false},
		{name: "fallback error", supported: false, fallbackErr: fallbackErr},
		{name: "meta_struct", supported: true, fallbackErr: fallbackErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := make(chan capturedMetaStructResult, 1)
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/info":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"endpoints":         []string{"/v0.4/traces"},
						"span_meta_structs": test.supported,
					})
				case "/v0.4/traces":
					span, ok, err := decodeMetaStructSpan(r)
					if ok || err != nil {
						captured <- capturedMetaStructResult{span: span, err: err}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"rate_by_service": map[string]float64{}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer agent.Close()

			tracer.Start(
				tracer.WithAgentAddr(strings.TrimPrefix(agent.URL, "http://")),
				tracer.WithLogStartup(false),
			)
			defer tracer.Stop()

			fallbackCalled := false
			span := tracer.StartSpan("metastruct.test")
			err := SetMetaStructTag(
				span,
				"structured",
				msgp.Raw(msgp.AppendString(nil, "value")),
				"fallback",
				func() (any, error) {
					fallbackCalled = true
					return "fallback-value", test.fallbackErr
				},
			)
			if !test.supported && test.fallbackErr != nil {
				require.ErrorIs(t, err, test.fallbackErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, !test.supported, fallbackCalled)

			span.Finish()
			tracer.Flush()

			select {
			case result := <-captured:
				require.NoError(t, result.err)
				switch {
				case test.supported:
					require.Equal(t, msgp.AppendString(nil, "value"), result.span.metaStruct["structured"])
					require.NotContains(t, result.span.meta, "fallback")
				case test.fallbackErr != nil:
					require.NotContains(t, result.span.metaStruct, "structured")
					require.NotContains(t, result.span.meta, "fallback")
				default:
					require.Equal(t, "fallback-value", result.span.meta["fallback"])
					require.NotContains(t, result.span.metaStruct, "structured")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the test agent to receive the span")
			}
		})
	}
}

func decodeMetaStructSpan(r *http.Request) (capturedMetaStructSpan, bool, error) {
	reader := msgp.NewReader(r.Body)
	traceCount, err := reader.ReadArrayHeader()
	if err != nil {
		return capturedMetaStructSpan{}, false, err
	}
	if traceCount == 0 {
		return capturedMetaStructSpan{}, false, nil
	}
	if traceCount != 1 {
		return capturedMetaStructSpan{}, false, fmt.Errorf("got %d traces, want 1", traceCount)
	}
	spanCount, err := reader.ReadArrayHeader()
	if err != nil {
		return capturedMetaStructSpan{}, false, err
	}
	if spanCount != 1 {
		return capturedMetaStructSpan{}, false, fmt.Errorf("got %d spans, want 1", spanCount)
	}

	fieldCount, err := reader.ReadMapHeader()
	if err != nil {
		return capturedMetaStructSpan{}, false, err
	}
	span := capturedMetaStructSpan{}
	for range fieldCount {
		field, err := reader.ReadString()
		if err != nil {
			return capturedMetaStructSpan{}, false, err
		}
		switch field {
		case "meta":
			span.meta, err = readStringMap(reader)
		case "meta_struct":
			span.metaStruct, err = readStringMap(reader)
		default:
			err = reader.Skip()
		}
		if err != nil {
			return capturedMetaStructSpan{}, false, err
		}
	}
	return span, true, nil
}

func readStringMap(reader *msgp.Reader) (map[string]any, error) {
	count, err := reader.ReadMapHeader()
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, count)
	for range count {
		key, err := reader.ReadString()
		if err != nil {
			return nil, err
		}
		value, err := reader.ReadIntf()
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}
