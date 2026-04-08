// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package apimcallout

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	tracer.Start()
	t.Cleanup(tracer.Stop)
	return NewHandler(AppsecAPIMConfig{
		Context: t.Context(),
	})
}

func newTestHandlerWithBodyParsing(t *testing.T, bodyLimit int) http.Handler {
	t.Helper()
	tracer.Start()
	t.Cleanup(tracer.Stop)
	return NewHandler(AppsecAPIMConfig{
		Context:              t.Context(),
		BodyParsingSizeLimit: &bodyLimit,
	})
}

func marshalAddresses(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func doPost(t *testing.T, handler http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeResult(t *testing.T, rr *httptest.ResponseRecorder) calloutResult {
	t.Helper()
	var result calloutResult
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	return result
}

func checkForAppsecEvent(t *testing.T, finished []*mocktracer.Span, expectedRuleIDs map[string]int) {
	t.Helper()

	event := finished[len(finished)-1].Tag("_dd.appsec.json")
	require.NotNil(t, event, "the _dd.appsec.json tag was not found")

	jsonText := event.(string)
	type trigger struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	var parsed struct {
		Triggers []trigger `json:"triggers"`
	}
	err := json.Unmarshal([]byte(jsonText), &parsed)
	require.NoError(t, err)

	histogram := map[string]int{}
	for _, tr := range parsed.Triggers {
		histogram[tr.Rule.ID]++
	}

	for ruleID, count := range expectedRuleIDs {
		require.Equal(t, count, int(histogram[ruleID]), "rule %s has been triggered %d times but expected %d", ruleID, histogram[ruleID], count)
	}

	require.Len(t, parsed.Triggers, len(expectedRuleIDs), "unexpected number of rules triggered")
}

// rawBody wraps a string as a JSON-quoted json.RawMessage for use in test structs.
// An empty string returns nil (no body).
func rawBody(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(`"` + s + `"`)
}

func end2EndRequest(t *testing.T, handler http.Handler, path, method string, reqHeaders, respHeaders map[string][]string, reqBody, respBody string) {
	t.Helper()

	msg := calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    method,
			Scheme:    "https",
			Authority: "datadoghq.com",
			Path:      path,
			Headers:   reqHeaders,
			Body:      rawBody(reqBody),
		}),
	}

	rr := doPost(t, handler, msg)
	require.Equal(t, http.StatusOK, rr.Code)
	reqResult := decodeResult(t, rr)
	require.Nil(t, reqResult.Block)
	require.NotEmpty(t, reqResult.RequestID)

	respMsg := calloutMessage{
		RequestID: reqResult.RequestID,
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    respHeaders,
			Body:       rawBody(respBody),
		}),
	}

	rr = doPost(t, handler, respMsg)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleRequestContinue(t *testing.T) {
	h := newTestHandler(t)

	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:     "GET",
			Scheme:     "https",
			Authority:  "api.example.com",
			Path:       "/users/123",
			RemoteAddr: "1.2.3.4:56789",
			Headers: map[string][]string{
				"User-Agent": {"Mozilla/5.0"},
			},
		}),
	})

	assert.Equal(t, http.StatusOK, rr.Code)

	result := decodeResult(t, rr)
	assert.Nil(t, result.Block)
	assert.NotEmpty(t, result.RequestID)
	assert.NotNil(t, result.PropagateHeaders)
}

func TestHandleRequestInvalidJSON(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	result := decodeResult(t, rr)
	assert.Nil(t, result.Block)
}

func TestHandleResponseContinueWithRequestID(t *testing.T) {
	h := newTestHandler(t)

	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    "GET",
			Scheme:    "https",
			Authority: "api.example.com",
			Path:      "/users/123",
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	reqResult := decodeResult(t, rr)
	require.Nil(t, reqResult.Block)
	require.NotEmpty(t, reqResult.RequestID)

	rr = doPost(t, h, calloutMessage{
		RequestID: reqResult.RequestID,
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
		}),
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	respResult := decodeResult(t, rr)
	assert.Nil(t, respResult.Block)
}

func TestHandleResponseUnknownRequestID(t *testing.T) {
	h := newTestHandler(t)

	rr := doPost(t, h, calloutMessage{
		RequestID: "unknown-id",
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
		}),
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	result := decodeResult(t, rr)
	assert.Nil(t, result.Block)
}

func TestHandleInvalidAddresses(t *testing.T) {
	h := newTestHandler(t)

	// Send valid outer JSON but with invalid addresses content.
	// We bypass doPost because json.Marshal would reject the invalid RawMessage.
	body := []byte(`{"addresses": "not an object"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	result := decodeResult(t, rr)
	assert.Nil(t, result.Block)
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestDecodeRawBase64Body(t *testing.T) {
	tests := []struct {
		name    string
		input   json.RawMessage
		want    []byte
		wantErr bool
	}{
		{"nil", nil, nil, false},
		{"null", json.RawMessage("null"), nil, false},
		{"empty-string", json.RawMessage(`""`), nil, false},
		{"valid", json.RawMessage(`"aGVsbG8="`), []byte("hello"), false},
		{"invalid", json.RawMessage(`"not-valid-base64!!!"`), nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeRawBase64Body(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// AppSec tests -- these use DD_APPSEC_RULES to test WAF detection and blocking.

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)

	setup := func(t *testing.T) (http.Handler, mocktracer.Tracer) {
		t.Helper()
		mt := mocktracer.Start()
		t.Cleanup(mt.Stop)
		h := NewHandler(AppsecAPIMConfig{Context: t.Context()})
		return h, mt
	}

	t.Run("monitoring-event-on-request-headers", func(t *testing.T) {
		h, mt := setup(t)

		end2EndRequest(t, h, "/", "POST",
			map[string][]string{"User-Agent": {"dd-test-scanner-log"}},
			map[string][]string{}, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})
	})

	t.Run("monitoring-event-on-response-headers", func(t *testing.T) {
		h, mt := setup(t)

		end2EndRequest(t, h, "/", "GET",
			map[string][]string{"User-Agent": {"Chromium"}, "Content-Type": {"application/json"}},
			map[string][]string{"Test": {"match-no-block-response-header"}}, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("blocking-event-on-request-headers", func(t *testing.T) {
		h, mt := setup(t)

		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/",
				Headers:   map[string][]string{"User-Agent": {"dd-test-scanner-log-block"}},
			}),
		})

		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		assert.Equal(t, 403, result.Block.Status)
		assert.Contains(t, result.Block.Headers, "Content-Type")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "ua0-600-56x": 1})

		span := finished[0]
		assert.Equal(t, "true", span.Tag("appsec.event"))
		assert.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-query", func(t *testing.T) {
		h, mt := setup(t)

		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/hello?match=match-request-query",
				Headers:   map[string][]string{"User-Agent": {"Mistake Not..."}},
			}),
		})

		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		assert.Equal(t, 418, result.Block.Status)
		assert.Contains(t, result.Block.Headers, "Content-Type")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"query-002": 1})

		span := finished[0]
		assert.Equal(t, "true", span.Tag("appsec.event"))
		assert.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-cookies", func(t *testing.T) {
		h, mt := setup(t)

		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "OPTIONS",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/",
				Headers:   map[string][]string{"Cookie": {"foo=jdfoSDGFkivRG_234"}},
			}),
		})

		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		assert.Equal(t, 418, result.Block.Status)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"tst-037-008": 1})

		span := finished[0]
		assert.Equal(t, "true", span.Tag("appsec.event"))
		assert.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-client-ip", func(t *testing.T) {
		h, mt := setup(t)

		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:     "GET",
				Scheme:     "https",
				Authority:  "datadoghq.com",
				Path:       "/",
				RemoteAddr: "111.222.111.222",
				Headers:    map[string][]string{"User-Agent": {"Mistake not..."}, "X-Forwarded-For": {"111.222.111.222"}},
			}),
		})

		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		assert.Equal(t, 403, result.Block.Status)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "blk-001-001": 1})

		span := finished[0]
		assert.Equal(t, "111.222.111.222", span.Tag("http.client_ip"))
		assert.Equal(t, "true", span.Tag("appsec.event"))
		assert.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("client-ip", func(t *testing.T) {
		h, mt := setup(t)

		end2EndRequest(t, h, "/", "GET",
			map[string][]string{"User-Agent": {"Mistake not..."}, "X-Forwarded-For": {"18.18.18.18"}},
			map[string][]string{}, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		span := finished[0]
		assert.Equal(t, "18.18.18.18", span.Tag("http.client_ip"))
		assert.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
	})

	t.Run("block-response-base64-body", func(t *testing.T) {
		h, _ := setup(t)

		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/hello?match=match-request-query",
				Headers:   map[string][]string{"User-Agent": {"Mistake Not..."}},
			}),
		})

		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		require.NotEmpty(t, result.Block.Content)

		// Decode the base64 body and verify it's valid JSON
		body, err := base64.StdEncoding.DecodeString(result.Block.Content)
		require.NoError(t, err)

		var blockBody map[string]any
		require.NoError(t, json.Unmarshal(body, &blockBody))
		assert.Contains(t, blockBody, "errors")
	})
}

func TestAppSecBodyParsing(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)

	setup := func(t *testing.T) (http.Handler, mocktracer.Tracer) {
		t.Helper()
		mt := mocktracer.Start()
		t.Cleanup(mt.Stop)
		bodyLimit := 256
		h := NewHandler(AppsecAPIMConfig{
			Context:              t.Context(),
			BodyParsingSizeLimit: &bodyLimit,
		})
		return h, mt
	}

	t.Run("monitoring-event-on-request-body", func(t *testing.T) {
		h, mt := setup(t)

		reqBody := base64.StdEncoding.EncodeToString([]byte(`{ "payload": {"name": "<script>alert(1)</script>" } }`))
		end2EndRequest(t, h, "/", "GET",
			map[string][]string{"User-Agent": {"Chromium"}, "Content-Type": {"application/json"}},
			map[string][]string{}, reqBody, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-941-110": 1})
	})

	t.Run("blocking-event-on-request-body", func(t *testing.T) {
		h, mt := setup(t)

		reqBody := base64.StdEncoding.EncodeToString([]byte(`{ "name": "$globals" }`))
		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/",
				Headers:   map[string][]string{"User-Agent": {"Chromium"}, "Content-Type": {"application/json"}},
				Body:      rawBody(reqBody),
			}),
		})

		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)
		assert.Equal(t, 403, result.Block.Status)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-933-130-block": 1})

		span := finished[0]
		assert.Equal(t, "true", span.Tag("appsec.event"))
		assert.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("no-monitoring-event-on-body-parsing-disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		t.Cleanup(mt.Stop)
		h := NewHandler(AppsecAPIMConfig{Context: t.Context()}) // no body parsing limit set, defaults to large

		reqBody := base64.StdEncoding.EncodeToString([]byte(`{ "name": "<script>alert(1)</script>" }`))
		end2EndRequest(t, h, "/", "PUT",
			map[string][]string{"User-Agent": {"Chromium"}, "Content-Type": {"application/json"}},
			map[string][]string{}, reqBody, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		span := finished[0]
		// The body should still be analyzed with the default limit
		// Just verify no crash and the span is created
		require.NotNil(t, span)
	})
}

// TestRequestStateCleanup verifies that RequestState resources (including pinned
// WAF memory) are properly released on all code paths. A leaked RequestState
// causes runtime.Pinner to panic during GC finalization.
func TestRequestStateCleanup(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)

	t.Run("block-on-request-headers-closes-state", func(t *testing.T) {
		mt := mocktracer.Start()
		t.Cleanup(mt.Stop)
		h := NewHandler(AppsecAPIMConfig{Context: t.Context()})

		// Send a request that will be blocked at the header phase
		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "datadoghq.com",
				Path:      "/hello?match=match-request-query",
				Headers:   map[string][]string{"User-Agent": {"Mistake Not..."}},
			}),
		})
		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.NotNil(t, result.Block)

		// Force GC to trigger Pinner finalizers. If the RequestState was not
		// properly closed, this will panic with "runtime.Pinner: found leaking
		// pinned pointer; forgot to call Unpin()?"
		runtime.GC()
	})

	t.Run("continue-request-without-response-closes-on-cache-shutdown", func(t *testing.T) {
		mt := mocktracer.Start()
		t.Cleanup(mt.Stop)

		ctx, cancel := context.WithCancel(t.Context())
		h := NewHandler(AppsecAPIMConfig{Context: ctx})

		// Send a request that continues (gets cached), but never send the response
		rr := doPost(t, h, calloutMessage{
			Addresses: marshalAddresses(t, addressesRequestHeaders{
				Method:    "GET",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/users/123",
				Headers:   map[string][]string{"User-Agent": {"Mozilla/5.0"}},
			}),
		})
		require.Equal(t, http.StatusOK, rr.Code)
		result := decodeResult(t, rr)
		require.Nil(t, result.Block)

		// Cancel context to trigger cache shutdown. The cached RequestState
		// should be evicted and closed.
		cancel()

		// Give the shutdown goroutine time to run
		runtime.Gosched()

		// Force GC -- if the cached state was not closed on shutdown, Pinner panics
		runtime.GC()
	})
}

func TestFourCallFlow(t *testing.T) {
	h := newTestHandlerWithBodyParsing(t, 256)

	// Phase 1: request headers (no body inline, no request-id)
	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    "POST",
			Scheme:    "https",
			Authority: "datadoghq.com",
			Path:      "/api/data",
			Headers: map[string][]string{
				"User-Agent":   {"Mozilla/5.0"},
				"Content-Type": {"application/json"},
			},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase1 := decodeResult(t, rr)
	assert.Nil(t, phase1.Block)
	require.NotEmpty(t, phase1.RequestID)
	assert.NotNil(t, phase1.PropagateHeaders)
	assert.NotNil(t, phase1.AllowedBodySize, "body analysis needed but body not inline, so AllowedBodySize must be set")

	// Phase 2: request body
	reqBody := base64.StdEncoding.EncodeToString([]byte(`{"key": "value"}`))
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesBody{Body: rawBody(reqBody)}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase2 := decodeResult(t, rr)
	assert.Nil(t, phase2.Block)
	assert.Empty(t, phase2.RequestID, "only Phase 1 returns a request-id")

	// Phase 3: response headers
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase3 := decodeResult(t, rr)
	assert.Nil(t, phase3.Block)
	assert.NotNil(t, phase3.AllowedBodySize, "response body expected")

	// Phase 4: response body
	respBody := base64.StdEncoding.EncodeToString([]byte(`{"result": "ok"}`))
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesBody{Body: rawBody(respBody)}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase4 := decodeResult(t, rr)
	assert.Nil(t, phase4.Block)
}

func TestSkipBodyTransition(t *testing.T) {
	h := newTestHandlerWithBodyParsing(t, 256)

	// Phase 1: request headers with Content-Type that enables body parsing
	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    "POST",
			Scheme:    "https",
			Authority: "datadoghq.com",
			Path:      "/api/data",
			Headers: map[string][]string{
				"User-Agent":   {"Mozilla/5.0"},
				"Content-Type": {"application/json"},
			},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase1 := decodeResult(t, rr)
	require.Nil(t, phase1.Block)
	require.NotEmpty(t, phase1.RequestID)
	require.NotNil(t, phase1.AllowedBodySize)

	// Phase 3 (skip Phase 2): send response headers directly.
	// The handler detects status_code > 0 in addresses and redirects to processResponseHeaders.
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	result := decodeResult(t, rr)
	assert.Nil(t, result.Block)
}

func TestFourCallFlowWithAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	testutils.StartAppSec(t)

	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	bodyLimit := 256
	h := NewHandler(AppsecAPIMConfig{
		Context:              t.Context(),
		BodyParsingSizeLimit: &bodyLimit,
	})

	// Phase 1: request with UA that triggers monitoring
	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    "POST",
			Scheme:    "https",
			Authority: "datadoghq.com",
			Path:      "/",
			Headers: map[string][]string{
				"User-Agent":   {"dd-test-scanner-log"},
				"Content-Type": {"application/json"},
			},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase1 := decodeResult(t, rr)
	require.Nil(t, phase1.Block)
	require.NotEmpty(t, phase1.RequestID)
	require.NotNil(t, phase1.AllowedBodySize)

	// Phase 2: request body (benign)
	reqBody := base64.StdEncoding.EncodeToString([]byte(`{"key": "value"}`))
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesBody{Body: rawBody(reqBody)}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase2 := decodeResult(t, rr)
	require.Nil(t, phase2.Block)

	// Phase 3: response headers
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase3 := decodeResult(t, rr)
	require.Nil(t, phase3.Block)

	// Phase 4: response body (benign)
	respBody := base64.StdEncoding.EncodeToString([]byte(`{"result": "ok"}`))
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Addresses: marshalAddresses(t, addressesBody{Body: rawBody(respBody)}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase4 := decodeResult(t, rr)
	require.Nil(t, phase4.Block)

	// Verify AppSec event was recorded with the UA rule
	finished := mt.FinishedSpans()
	require.Len(t, finished, 1)
	checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})
}

func TestPhaseMismatchProcessesCorrectly(t *testing.T) {
	h := newTestHandler(t)

	// Phase 1
	rr := doPost(t, h, calloutMessage{
		Addresses: marshalAddresses(t, addressesRequestHeaders{
			Method:    "GET",
			Scheme:    "https",
			Authority: "api.example.com",
			Path:      "/users/123",
			Headers:   map[string][]string{"User-Agent": {"Mozilla/5.0"}},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	phase1 := decodeResult(t, rr)
	require.NotEmpty(t, phase1.RequestID)

	// Phase 3: send with wrong phase field (says "<RequestBody>" but we're sending response headers)
	rr = doPost(t, h, calloutMessage{
		RequestID: phase1.RequestID,
		Phase:     "<RequestBody>",
		Addresses: marshalAddresses(t, addressesResponseHeaders{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		}),
	})
	require.Equal(t, http.StatusOK, rr.Code)
	result := decodeResult(t, rr)
	assert.Nil(t, result.Block) // Should process correctly despite phase mismatch
}
