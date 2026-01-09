// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/message"
	"github.com/negasus/haproxy-spoe-go/payload/kv"
	"github.com/negasus/haproxy-spoe-go/request"

	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)

	setup := func() (func(req *request.Request), mocktracer.Tracer, func()) {
		rig := newHAProxyAppsecRig(t, false, 0)
		mt := mocktracer.Start()

		return rig.handler, mt, func() {
			mt.Stop()
		}
	}

	t.Run("monitoring-event-on-request-headers", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "POST", map[string]string{"User-Agent": "dd-test-scanner-log"}, map[string]string{}, false, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})
	})
	t.Run("monitoring-event-on-response-headers-without-body", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})
	t.Run("blocking-event-on-request-headers", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		_, _, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "dd-test-scanner-log-block"}, "GET", "/")

		require.Equal(t, 403, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "ua0-600-56x": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
	t.Run("blocking-event-on-request-on-query", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		_, _, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Mistake Not..."}, "GET", "/hello?match=match-request-query")

		require.Equal(t, 418, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"query-002": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
	t.Run("blocking-event-on-request-on-cookies", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		_, _, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"Cookie": "foo=jdfoSDGFkivRG_234"}, "OPTIONS", "/")

		require.Equal(t, 418, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"tst-037-008": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("client-ip", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "OPTION",
			map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "18.18.18.18"},
			map[string]string{"User-Agent": "match-response-header"},
			true, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "18.18.18.18", span.Tag("http.client_ip"))

		// Appsec
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
	})

	t.Run("blocking-client-ip", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		_, _, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "111.222.111.222"}, "GET", "/")

		// Handle the immediate response
		require.Equal(t, 403, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "blk-001-001": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "111.222.111.222", span.Tag("http.client_ip"))
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
	t.Run("no-monitoring-event-on-request-body-parsing-disabled", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, `{ "name": "<script>alert(1)</script>" }`, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check that no appsec event was created
		span := finished[0]
		require.NotContains(t, span.Tags(), "appsec.event")
		require.NotContains(t, span.Tags(), "_dd.appsec.json")
	})
}

func TestAppSecBodyParsingEnabled(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)

	setup := func() (func(req *request.Request), mocktracer.Tracer, func()) {
		rig := newHAProxyAppsecRig(t, false, 256)
		mt := mocktracer.Start()

		return rig.handler, mt, func() {
			mt.Stop()
		}
	}
	t.Run("monitoring-event-on-request-body", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, `{ "payload": {"name": "<script>alert(1)</script>" } }`, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-941-110": 1})
	})

	t.Run("monitoring-event-on-response-headers-without-body", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})
	t.Run("monitoring-event-on-response-headers-with-body-sent", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "body")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("monitoring-event-on-response-headers-with-body-not-sent", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		spanId, requestedRequestBody, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json", "Content-Length": "0"}, "GET", "/")
		require.False(t, requestedRequestBody)
		require.Nil(t, blockedAct)

		// Send a processing response headers with the information that it would be followed by a body, but don't send the body
		// It is mimicking a scenario where the response headers are sent and a body is present, but response body processing is disabled in the HAProxy configuration
		requestedResponseBody, blockedAct := sendProcessingResponseHeaders(t, handler, map[string]string{"test": "match-no-block-response-header", "Content-Type": "application/json"}, "200", spanId)
		require.True(t, requestedResponseBody)
		require.Nil(t, blockedAct)

		// Not timed out yet, so no span finished
		finished := mt.FinishedSpans()
		require.Len(t, finished, 0)

		// Will timeouts because a body was expected but not sent
		// (wait for 2 seconds to ensure the timeout of 1s happens and the trace is closed)
		time.Sleep(2 * time.Second)

		finished = mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("blocking-event-on-request-body", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		body := []byte(`{ "name": "$globals" }`)
		spanId, requestedRequestBody, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/")
		require.True(t, requestedRequestBody)
		require.Nil(t, blockedAct)

		blockedAct = sendProcessingRequestBody(t, handler, body, spanId)
		require.NotNil(t, blockedAct)

		require.Equal(t, 403, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-933-130-block": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-response-headers-without-body", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "OPTION", map[string]string{"User-Agent": "Chrome"}, map[string]string{"test": "match-response-header"}, true, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"headers-003": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-response-headers-with-body-sent", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		// Blocking on response body and not on response headers, because the afterHandle is not called when the processor is waiting for a body
		end2EndStreamRequest(t, handler, "/", "OPTION", map[string]string{"User-Agent": "Chrome"}, map[string]string{"content-type": "application/json", "test": "match-response-header"}, false, true, "", "{ \"name\": \"test\" }")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"headers-003": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
	t.Run("no-monitoring-event-on-request-body-bad-content-type", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "text/html"}, map[string]string{}, false, false, `{ "name": "<script>alert(1)</script>" }`, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		span := finished[0]
		require.NotContains(t, span.Tags(), "appsec.event")
		require.NotContains(t, span.Tags(), "_dd.appsec.json")
	})
	t.Run("blocking-event-on-request-body-truncated", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		largeText := make([]byte, 300)
		for i := range largeText {
			largeText[i] = 'x'
		}
		requestBody := fmt.Sprintf(`{ "name": "$globals", "text": "%s" }`, largeText)

		spanId, bodyRequested, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/")
		require.True(t, bodyRequested)
		require.Nil(t, blockedAct)

		// Should block at the first chunk
		blockedAct = sendProcessingRequestBody(t, handler, []byte(requestBody), spanId)
		require.NotNil(t, blockedAct)

		require.Equal(t, 403, blockedAct.statusCode)
		require.Equal(t, "application/json", blockedAct.headers["Content-Type"])

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-933-130-block": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
	t.Run("no-blocking-event-on-request-body-attack-truncated", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		largeText := make([]byte, 300)
		for i := range largeText {
			largeText[i] = 'x'
		}
		requestBody := fmt.Sprintf(`{ "text": "%s", "name": "$globals" }`, largeText)

		end2EndStreamRequest(t, handler, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, requestBody, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.NotContains(t, span.Tags(), "appsec.event")
		require.NotContains(t, span.Tags(), "_dd.appsec.json")
	})

	// NOTE: This test simulates a scenario where the response body is never sent, even though it was requested by the processor.
	// In reality, if HAProxy fails to send the body (e.g., due to a timeout or backend error), the processor times out and marks the request as blocked in the trace.
	// However, this does not necessarily reflect what the client actually received, since we have no visibility into the real response.
	// This test is validating this internal timeout/blocking behavior, not the actual client experience.
	t.Run("blocking-event-on-response-headers-with-body-not-sent", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		spanId, bodyRequested, _, blockedAct := sendProcessingRequestHeaders(t, handler, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/")
		require.True(t, bodyRequested)
		require.Nil(t, blockedAct)

		// Send a processing response headers with the information that it would be followed by a body, but don't send the body
		bodyRequested, blockedAct = sendProcessingResponseHeaders(t, handler, map[string]string{"test": "match-response-header", "Content-Type": "application/json"}, "200", spanId)

		// Res should be an immediate response with the blocking event
		require.Nil(t, blockedAct)
		require.True(t, bodyRequested)

		time.Sleep(2 * time.Second)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-003": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
}

func TestAppSecAPISecurityBodyParsingEnabled(t *testing.T) {
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")
	t.Setenv("_DD_APPSEC_PROXY_ENVIRONMENT", "true") // Enable API Security proxy sampler

	testutils.StartAppSec(t)

	setup := func() (func(req *request.Request), mocktracer.Tracer, func()) {
		rig := newHAProxyAppsecRig(t, false, 256)
		mt := mocktracer.Start()

		return rig.handler, mt, func() {
			mt.Stop()
		}
	}

	t.Run("only-headers-schema-returned", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium"}, map[string]string{"Content-Type": "text/html"}, false, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for api security schema
		span := finished[0]
		require.Equal(t, `[{"host":[[[8]],{"len":1}],"user-agent":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.req.headers"))
		require.Nil(t, span.Tag("_dd.appsec.s.req.body"))
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.res.headers"))
		require.Nil(t, span.Tag("_dd.appsec.s.res.body"))
	})

	t.Run("headers-and-req-body-schema-returned", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"Content-Type": "text/html"}, false, false, `{"hello_request_body":"world"}`, "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for api security schema
		span := finished[0]
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}],"host":[[[8]],{"len":1}],"user-agent":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.req.headers"))
		require.Equal(t, `[{"hello_request_body":[8]}]`, span.Tag("_dd.appsec.s.req.body"))
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.res.headers"))
		require.Nil(t, span.Tag("_dd.appsec.s.res.body"))
	})

	t.Run("headers-and-res-body-schema-returned", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium"}, map[string]string{"Content-Type": "application/json"}, false, false, "", `{"hello_response_body": "world"}`)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for api security schema
		span := finished[0]
		require.Equal(t, `[{"host":[[[8]],{"len":1}],"user-agent":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.req.headers"))
		require.Nil(t, span.Tag("_dd.appsec.s.req.body"))
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.res.headers"))
		require.Equal(t, `[{"hello_response_body":[8]}]`, span.Tag("_dd.appsec.s.res.body"))
	})

	t.Run("all-schema-returned", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"Content-Type": "application/json"}, false, false, `{"hello_request_body":"world"}`, `{"hello_response_body": "world"}`)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for api security schema
		span := finished[0]
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}],"host":[[[8]],{"len":1}],"user-agent":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.req.headers"))
		require.Equal(t, `[{"hello_request_body":[8]}]`, span.Tag("_dd.appsec.s.req.body"))
		require.Equal(t, `[{"content-type":[[[8]],{"len":1}]}]`, span.Tag("_dd.appsec.s.res.headers"))
		require.Equal(t, `[{"hello_response_body":[8]}]`, span.Tag("_dd.appsec.s.res.body"))
	})
}

func TestGeneratedSpan(t *testing.T) {
	setup := func() (func(req *request.Request), mocktracer.Tracer, func()) {
		rig := newHAProxyAppsecRig(t, false, 0)
		mt := mocktracer.Start()

		return rig.handler, mt, func() {
			mt.Stop()
		}
	}

	t.Run("request-span", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/../../../resource-span/.?id=test", "GET", map[string]string{"user-agent": "Mistake Not...", "test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false, false, "", "body")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "http.request", span.OperationName())
		require.Equal(t, "https://datadoghq.com/../../../resource-span/.?id=test", span.Tag("http.url"))
		require.Equal(t, "GET", span.Tag("http.method"))
		require.Equal(t, "datadoghq.com", span.Tag("http.host"))
		require.Equal(t, "GET /resource-span", span.Tag("resource.name"))
		require.Equal(t, "server", span.Tag("span.kind"))
		require.Equal(t, "Mistake Not...", span.Tag("http.useragent"))
		require.Equal(t, "haproxy-spoa", span.Tag("component"))
	})
	t.Run("span-with-injected-context", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		// add metadata to the context
		headers := map[string]string{
			"User-Agent":          "Mistake Not...",
			"Test-Key":            "test-value",
			"x-datadog-trace-id":  "12345",
			"x-datadog-parent-id": "67890",
		}

		end2EndStreamRequest(t, handler, "/../../../resource-span/.?id=test", "GET", headers, map[string]string{"response-test-key": "response-test-value"}, false, false, "", "body")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "http.request", span.OperationName())
		require.Equal(t, "https://datadoghq.com/../../../resource-span/.?id=test", span.Tag("http.url"))
		require.Equal(t, "GET", span.Tag("http.method"))
		require.Equal(t, "datadoghq.com", span.Tag("http.host"))
		require.Equal(t, "GET /resource-span", span.Tag("resource.name"))
		require.Equal(t, "server", span.Tag("span.kind"))
		require.Equal(t, "Mistake Not...", span.Tag("http.useragent"))
		require.Equal(t, "haproxy-spoa", span.Tag("component"))

		// Check for trace context
		require.Equal(t, "00000000000000000000000000003039", span.Context().TraceID())
		require.Equal(t, uint64(67890), span.ParentID())
	})
	t.Run("span-with-propagation-headers-set", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		spanId, _, injectedHeaders, _ := sendProcessingRequestHeaders(t, handler, map[string]string{}, "GET", "/../../../resource-span/.?id=test")

		// Check for trace propagation headers injected
		require.Contains(t, injectedHeaders, "tracing_x_datadog_trace_id")
		require.Contains(t, injectedHeaders, "tracing_x_datadog_parent_id")
		require.Contains(t, injectedHeaders, "tracing_x_datadog_tags")

		sendProcessingResponseHeaders(t, handler, nil, "200", spanId)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "http.request", span.OperationName())
		require.Equal(t, "https://datadoghq.com/../../../resource-span/.?id=test", span.Tag("http.url"))
		require.Equal(t, "GET", span.Tag("http.method"))
		require.Equal(t, "datadoghq.com", span.Tag("http.host"))
		require.Equal(t, "GET /resource-span", span.Tag("resource.name"))
		require.Equal(t, "server", span.Tag("span.kind"))
		require.Equal(t, "haproxy-spoa", span.Tag("component"))
	})
}

func TestMalformedHAProxyProcessing(t *testing.T) {
	testutils.StartAppSec(t)

	setup := func() (func(req *request.Request), mocktracer.Tracer, func()) {
		rig := newHAProxyAppsecRig(t, false, 0)
		mt := mocktracer.Start()

		return rig.handler, mt, func() {
			mt.Stop()
		}
	}

	t.Run("response-received-without-request", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		requestedBody, blockedAct := sendProcessingResponseHeaders(t, handler, map[string]string{}, "400", "0")
		require.False(t, requestedBody)
		require.Nil(t, blockedAct)

		// No span created, the request is invalid.
		// span couldn't be created without request data
		finished := mt.FinishedSpans()
		require.Len(t, finished, 0)
	})
	t.Run("unknown-url-escape-sequence-one", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		end2EndStreamRequest(t, handler, "/%u002e/resource", "GET", nil, nil, false, false, "", "")

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
	})
	t.Run("unknown-url-escape-sequence-six", func(t *testing.T) {
		handler, mt, cleanup := setup()
		defer cleanup()

		spanId, requestBody, _, blockedAct := sendProcessingRequestHeaders(t, handler, nil, "GET", "/%u002e/%ZZ/%tt/%uuuu/%uwu/%%")
		require.False(t, requestBody)
		require.Nil(t, blockedAct)
		require.Empty(t, spanId)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 0)
	})
}

func newHAProxyAppsecRig(t *testing.T, blockingUnavailable bool, bodyParsingSizeLimit int) *haproxyAppsecRig {
	t.Helper()

	if blockingUnavailable {
		_ = os.Setenv("_DD_APPSEC_BLOCKING_UNAVAILABLE", "true")
	}

	appsecHAProxy := NewHAProxySPOA(AppsecHAProxyConfig{
		BlockingUnavailable:  false,
		BodyParsingSizeLimit: bodyParsingSizeLimit,
		Context:              context.Background(),
	})

	return &haproxyAppsecRig{
		handler: appsecHAProxy.Handler,
	}
}

// rig contains all servers and connections we'd need for a haproxy integration test
type haproxyAppsecRig struct {
	handler func(req *request.Request)
}

// Helper functions

func sendProcessingRequestHeaders(t *testing.T, handler func(*request.Request), headers map[string]string, method string, path string) (string, bool, map[string]string, *blockedAction) {
	t.Helper()

	if headers == nil {
		headers = map[string]string{}
	}

	// Only for the test: specify the Host header
	if _, ok := headers["Host"]; !ok {
		headers["Host"] = "datadoghq.com"
	}

	mKv := kv.NewKV()
	mKv.Add(VarMethod, method)
	mKv.Add(VarPath, path)
	mKv.Add(VarHeaders, convertBinaryHeaders(headers))
	mKv.Add(VarHttps, true)
	mKv.Add(VarTimeout, "1s")

	if ip, ok := headers["X-Forwarded-For"]; ok {
		mKv.Add(VarIp, net.ParseIP(ip))
	} else {
		mKv.Add(VarIp, net.ParseIP("123.123.123.123"))
	}

	mKv.Add(VarIpPort, 12345)

	messages := message.Messages{
		&message.Message{Name: "http-request-headers-msg", KV: mKv},
	}

	actions := action.Actions{}

	pRequest := request.Request{
		EngineID: "test-engine",
		StreamID: 1,
		FrameID:  1,
		Messages: &messages,
		Actions:  actions,
	}

	handler(&pRequest)

	// Handle the response
	blockedAct, err := createBlockedAction(pRequest.Actions)
	require.NoError(t, err)
	if blockedAct != nil {
		return "", false, nil, blockedAct
	}

	spanId, err := findVar(pRequest.Actions, VarSpanId)
	if err != nil {
		return "", false, nil, nil
	}

	requestedBody, err := findVar(pRequest.Actions, VarRequestBody)
	if err != nil {
		requestedBody = false
	}

	// Handle injected headers
	injectedValues := make(map[string]string, len(haproxyTracingHeaderActions))
	for _, actionName := range haproxyTracingHeaderActions {
		if v, err := findVar(pRequest.Actions, actionName); err == nil {
			injectedValues[actionName] = v.(string)
		}
	}

	return spanId.(string), requestedBody.(bool), injectedValues, nil
}

// sendProcessingRequestBody sends the request body
func sendProcessingRequestBody(t *testing.T, handler func(*request.Request), body []byte, spanId string) *blockedAction {
	t.Helper()

	mKv := kv.NewKV()
	mKv.Add(VarBody, body)
	mKv.Add(VarSpanId, spanId)

	messages := message.Messages{
		&message.Message{Name: "http-request-body-msg", KV: mKv},
	}

	actions := action.Actions{}

	pRequest := request.Request{
		EngineID: "test-engine",
		StreamID: 1,
		FrameID:  1,
		Messages: &messages,
		Actions:  actions,
	}

	handler(&pRequest)

	// Handle the response
	blockedAct, err := createBlockedAction(pRequest.Actions)
	require.NoError(t, err)
	if blockedAct != nil {
		return blockedAct
	}

	return nil
}

func sendProcessingResponseHeaders(t *testing.T, handler func(*request.Request), headers map[string]string, status string, spanId string) (bool, *blockedAction) {
	t.Helper()

	mKv := kv.NewKV()
	mKv.Add(VarHeaders, convertBinaryHeaders(headers))
	mKv.Add(VarStatus, status)
	mKv.Add(VarSpanId, spanId)

	messages := message.Messages{
		&message.Message{Name: "http-response-headers-msg", KV: mKv},
	}

	actions := action.Actions{}

	pRequest := request.Request{
		EngineID: "test-engine",
		StreamID: 1,
		FrameID:  1,
		Messages: &messages,
		Actions:  actions,
	}

	handler(&pRequest)

	// Handle the response
	blockedAct, err := createBlockedAction(pRequest.Actions)
	require.NoError(t, err)
	if blockedAct != nil {
		return false, blockedAct
	}

	requestedBody, err := findVar(pRequest.Actions, VarRequestBody)
	if err != nil {
		requestedBody = false
	}

	return requestedBody.(bool), nil
}

// sendProcessingResponseBody sends the response body in chunks to the stream.
// Returns the total number of message chunks sent.
func sendProcessingResponseBody(t *testing.T, handler func(*request.Request), body []byte, spanId string) *blockedAction {
	t.Helper()

	mKv := kv.NewKV()
	mKv.Add(VarBody, body)
	mKv.Add(VarSpanId, spanId)

	messages := message.Messages{
		&message.Message{Name: "http-response-body-msg", KV: mKv},
	}

	actions := action.Actions{}

	pRequest := request.Request{
		EngineID: "test-engine",
		StreamID: 1,
		FrameID:  1,
		Messages: &messages,
		Actions:  actions,
	}

	handler(&pRequest)

	// Handle the response
	blockedAct, err := createBlockedAction(pRequest.Actions)
	require.NoError(t, err)
	if blockedAct != nil {
		return blockedAct
	}

	return nil
}

func convertBinaryHeaders(headers map[string]string) []byte {
	var b []byte
	for name, value := range headers {
		b = encodeHeader(b, name, value)
	}
	return encodeTerminator(b)
}

type blockedAction struct {
	headers    map[string]string
	body       []byte
	statusCode int
}

func createBlockedAction(actions action.Actions) (*blockedAction, error) {
	blocked, err := findVar(actions, VarBlocked)
	if err != nil || !blocked.(bool) {
		return nil, nil
	}

	headers, err := findVar(actions, VarHeaders)
	if err != nil {
		return nil, fmt.Errorf("blocked action without headers: %v", err)
	}

	parsedHeaders, err := parseBlockedHeaders(headers.(string))
	if err != nil {
		return nil, fmt.Errorf("blocked action with invalid headers: %v", err)
	}

	body, err := findVar(actions, VarBody)
	if err != nil {
		return nil, fmt.Errorf("blocked action without body: %v", err)
	}

	statusCode, err := findVar(actions, VarStatus)
	if err != nil {
		return nil, fmt.Errorf("blocked action without status code: %v", err)
	}

	return &blockedAction{
		headers:    parsedHeaders,
		body:       body.([]byte),
		statusCode: statusCode.(int),
	}, nil
}

func findVar(actions action.Actions, name string) (interface{}, error) {
	for _, a := range actions {
		if a.Type == action.TypeSetVar && a.Name == name {
			return a.Value, nil
		}
	}

	return nil, fmt.Errorf("variable %s not found in actions", name)
}

var (
	lineRegex = regexp.MustCompile(`(?m)^[^\r\n]+`)
	kvRegex   = regexp.MustCompile(`^([A-Za-z0-9-]+): (\S.+)$`)
)

func parseBlockedHeaders(s string) (map[string]string, error) {
	h := make(map[string]string)

	for _, line := range lineRegex.FindAllString(s, -1) {
		if m := kvRegex.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			h[key] = val
		} else {
			return nil, fmt.Errorf("invalid header line: %s", line)
		}
	}

	return h, nil
}

func end2EndStreamRequest(t *testing.T, handler func(*request.Request), path string, method string, requestHeaders map[string]string, responseHeaders map[string]string, blockOnResponseHeaders bool, blockOnResponseBody bool, requestBody string, responseBody string) {
	t.Helper()

	// First part: request
	// 1- Send the headers
	spanId, requestBodyRequested, _, blocked := sendProcessingRequestHeaders(t, handler, requestHeaders, method, path)
	require.Nil(t, blocked, "expected no blocked action when sending request headers")

	require.NotEmpty(t, spanId)

	// 2- Send the body: send it if the processor requested the body for analysis
	if requestBodyRequested && len(requestBody) > 0 {
		blocked := sendProcessingRequestBody(t, handler, []byte(requestBody), spanId)
		require.Nil(t, blocked, "expected no blocked action when sending request body")
	}

	// Second part: response
	// 1- Send the response headers
	responseBodyRequested, blocked := sendProcessingResponseHeaders(t, handler, responseHeaders, "200", spanId)
	if blockOnResponseHeaders {
		require.NotNil(t, blocked, "expected a blocked action when sending response headers")
		return
	} else {
		require.Nil(t, blocked, "expected no blocked action when sending request headers")
	}

	// 2- Send the body: send it if the processor requested the body for analysis
	if responseBodyRequested && len(responseBody) > 0 {
		blocked := sendProcessingResponseBody(t, handler, []byte(responseBody), spanId)
		if blockOnResponseBody {
			require.NotNil(t, blocked, "expected a blocked action when sending response body")
		} else {
			require.Nil(t, blocked, "expected no blocked action when sending response body")
		}
	}
}

func checkForAppsecEvent(t *testing.T, finished []*mocktracer.Span, expectedRuleIDs map[string]int) {
	t.Helper()

	// The request should have the attack attempts
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

	histogram := map[string]uint8{}
	for _, tr := range parsed.Triggers {
		histogram[tr.Rule.ID]++
	}

	for ruleID, count := range expectedRuleIDs {
		require.Equal(t, count, int(histogram[ruleID]), "rule %s has been triggered %d times but expected %d", ruleID, histogram[ruleID], count)
	}

	require.Len(t, parsed.Triggers, len(expectedRuleIDs), "unexpected number of rules triggered")
}
