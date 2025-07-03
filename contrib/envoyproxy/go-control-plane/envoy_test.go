// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocontrolplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	envoyextprocfilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "10ms")

	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false, 0)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("monitoring-event-on-request-headers", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "POST", map[string]string{"User-Agent": "dd-test-scanner-log"}, map[string]string{}, false, false, "", "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})
	})

	t.Run("monitoring-event-on-response-headers-without-body", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("blocking-event-on-request-headers", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "dd-test-scanner-log-block"}, "GET", "/", false)

		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "ua0-600-56x": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-request-on-query", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Mistake Not..."}, "GET", "/hello?match=match-request-query", false)

		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"query-002": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-request-on-cookies", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"Cookie": "foo=jdfoSDGFkivRG_234"}, "OPTIONS", "/", false)

		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"tst-037-008": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("client-ip", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "OPTION",
			map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "18.18.18.18"},
			map[string]string{"User-Agent": "match-response-header"},
			true, false, "", "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "18.18.18.18", span.Tag("http.client_ip"))

		// Appsec
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
	})

	t.Run("blocking-client-ip", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "111.222.111.222"}, "GET", "/", false)

		// Handle the immediate response
		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, `{ "name": "<script>alert(1)</script>" }`, "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false, 256)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("monitoring-event-on-request-body", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, `{ "name": "<script>alert(1)</script>" }`, "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-941-110": 1})
	})

	t.Run("monitoring-event-on-response-headers-without-body", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("monitoring-event-on-response-headers-with-body-sent", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{"test": "match-no-block-response-header"}, false, false, "", "body")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("monitoring-event-on-response-headers-with-body-not-sent", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/", false)
		_, err = stream.Recv()
		require.NoError(t, err)

		// Send a processing response headers with the information that it would be followed by a body, but don't send the body
		// It is mimicking a scenario where the response headers are sent and a body is present, but response body processing is disabled in the Envoy configuration
		sendProcessingResponseHeaders(t, stream, map[string]string{"test": "match-no-block-response-header", "Content-Type": "application/json"}, "200", true)
		_, err = stream.Recv()
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-004": 1})
	})

	t.Run("blocking-event-on-request-body", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/", true)
		res, err := stream.Recv()
		require.NoError(t, err)

		msgCount := sendProcessingRequestBodyStreamed(t, stream, []byte(`{ "name": "$globals" }`), 4)
		for i := 0; i < msgCount-1; i++ {
			res, err = stream.Recv()
			require.NoError(t, err)
		}

		res, err = stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-933-130-block": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-response-headers-without-body", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "OPTION", map[string]string{"User-Agent": "Chrome"}, map[string]string{"test": "match-response-header"}, true, false, "", "")

		// Handle the immediate response
		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code) // 418 because of the rule file
		require.Len(t, res.GetImmediateResponse().GetHeaders().SetHeaders, 1)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "OPTION", map[string]string{"User-Agent": "Chrome"}, map[string]string{"test": "match-response-header"}, false, true, "", "body")

		// Handle the immediate response
		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code) // 418 because of the rule file
		require.Len(t, res.GetImmediateResponse().GetHeaders().SetHeaders, 1)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "text/html"}, map[string]string{}, false, false, `{ "name": "<script>alert(1)</script>" }`, "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		span := finished[0]
		require.NotContains(t, span.Tags(), "appsec.event")
		require.NotContains(t, span.Tags(), "_dd.appsec.json")
	})

	t.Run("blocking-event-on-request-body-truncated", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/", true)
		res, err := stream.Recv()
		require.NoError(t, err)

		largeText := make([]byte, 300)
		for i := range largeText {
			largeText[i] = 'x'
		}
		requestBody := fmt.Sprintf(`{ "name": "$globals", "text": "%s" }`, largeText)

		// Should block at the first chunk
		_ = sendProcessingRequestBodyStreamed(t, stream, []byte(requestBody), 300)

		res, err = stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "crs-933-130-block": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("no-blocking-event-on-request-body-attack-truncated", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		largeText := make([]byte, 300)
		for i := range largeText {
			largeText[i] = 'x'
		}
		requestBody := fmt.Sprintf(`{ "text": "%s", "name": "$globals" }`, largeText)

		end2EndStreamRequest(t, stream, "/", "PUT", map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, map[string]string{}, false, false, requestBody, "")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.NotContains(t, span.Tags(), "appsec.event")
		require.NotContains(t, span.Tags(), "_dd.appsec.json")
	})

	// This test is failing because the external processor is waiting for a body to run the waf on the response headers
	// This scenario only happen if the Envoy configuration doesn't allow the mode override, and so Envoy never sends the body
	/*t.Run("blocking-event-on-response-headers-with-body-not-sent", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, map[string]string{"User-Agent": "Chromium", "Content-Type": "application/json"}, "GET", "/", false)
		_, err = stream.Recv()
		require.NoError(t, err)

		// Send a processing response headers with the information that it would be followed by a body, but don't send the body
		// It is mimicking a scenario where the response headers are sent and a body is present, but response body processing is disabled in the Envoy configuration
		sendProcessingResponseHeaders(t, stream, map[string]string{"test": "match-response-header", "Content-Type": "application/json"}, "200", true)

		// Res should be an immediate response with the blocking event
		res, err := stream.Recv()
		require.IsType(t, &envoyextproc.ProcessingResponse_ImmediateResponse{}, res.GetResponse())
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"custom-001": 1, "headers-003": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})*/

}

func TestGeneratedSpan(t *testing.T) {
	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false, 0)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("request-span", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/../../../resource-span/.?id=test", "GET", map[string]string{"user-agent": "Mistake Not...", "test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false, false, "", "body")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
		require.Equal(t, "envoyproxy/go-control-plane", span.Tag("component"))
	})

	t.Run("span-with-injected-context", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()

		// add metadata to the context
		ctx = metadata.AppendToOutgoingContext(ctx,
			"x-datadog-trace-id", "12345",
			"x-datadog-parent-id", "67890",
		)

		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/../../../resource-span/.?id=test", "GET", map[string]string{"user-agent": "Mistake Not...", "test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false, false, "", "body")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

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
		require.Equal(t, "envoyproxy/go-control-plane", span.Tag("component"))

		// Check for trace context
		require.Equal(t, "00000000000000000000000000003039", span.Context().TraceID())
		require.Equal(t, uint64(67890), span.ParentID())
	})
}

func TestMalformedEnvoyProcessing(t *testing.T) {
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false, 0)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("response-received-without-request", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingResponseHeaders(t, stream, map[string]string{}, "400", false)

		_, err = stream.Recv()
		require.Error(t, err)
		_, _ = stream.Recv()

		// No span created, the request is invalid.
		// Span couldn't be created without request data
		finished := mt.FinishedSpans()
		require.Len(t, finished, 0)
	})

	t.Run("unknown-url-escape-sequence-one", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/%u002e/resource", "GET", nil, nil, false, false, "", "")

		_, err = stream.Recv()
		require.Error(t, err)
		_, _ = stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
	})

	t.Run("unknown-url-escape-sequence-six", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		sendProcessingRequestHeaders(t, stream, nil, "GET", "/%u002e/%ZZ/%tt/%uuuu/%uwu/%%", false)

		_, err = stream.Recv()
		require.Error(t, err)
		_, _ = stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 0)
	})
}

func TestAppSecAsGCPServiceExtension(t *testing.T) {
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, true, false, 0)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("gcp-se-component-monitoring-event-on-request", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "dd-test-scanner-log"}, map[string]string{}, false, false, "", "body")

		err = stream.CloseSend()
		require.NoError(t, err)
		_, _ = stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})

		// Check for component tag
		span := finished[0]
		require.Equal(t, "gcp-service-extension", span.Tag("component"))
	})
}

func newEnvoyAppsecRig(t *testing.T, traceClient bool, isGCPServiceExtension bool, blockingUnavailable bool, bodyParsingSizeLimit int) (*envoyAppsecRig, error) {
	t.Helper()

	server := grpc.NewServer()
	fixtureServer := new(envoyFixtureServer)

	if blockingUnavailable {
		_ = os.Setenv("_DD_APPSEC_BLOCKING_UNAVAILABLE", "true")
	}

	var appsecSrv envoyextproc.ExternalProcessorServer
	appsecSrv = AppsecEnvoyExternalProcessorServer(fixtureServer, AppsecEnvoyConfig{
		IsGCPServiceExtension: isGCPServiceExtension,
		BlockingUnavailable:   blockingUnavailable,
		BodyParsingSizeLimit:  bodyParsingSizeLimit,
	})

	envoyextproc.RegisterExternalProcessorServer(server, appsecSrv)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test fixtureServer.
	go func() {
		if server.Serve(li) != nil {
			t.Errorf("error serving: %s", err)
		}
	}()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	conn, err := grpc.NewClient(li.Addr().String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &envoyAppsecRig{
		fixtureServer: fixtureServer,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        envoyextproc.NewExternalProcessorClient(conn),
	}, err
}

// rig contains all servers and connections we'd need for a grpc integration test
type envoyAppsecRig struct {
	fixtureServer *envoyFixtureServer
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        envoyextproc.ExternalProcessorClient
}

func (r *envoyAppsecRig) Close() {
	r.server.Stop()
	_ = r.conn.Close()
}

type envoyFixtureServer struct {
	envoyextproc.ExternalProcessorServer
}

// Helper functions

func sendProcessingRequestHeaders(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, headers map[string]string, method string, path string, hasBody bool) {
	t.Helper()

	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestHeaders{
			RequestHeaders: &envoyextproc.HttpHeaders{
				Headers:     makeRequestHeaders(t, headers, method, path),
				EndOfStream: !hasBody,
			},
		},
	})
	require.NoError(t, err)
}

// sendProcessingRequestBodyStreamed sends the request body in chunks to simulate streaming
// Returns the total number of message chunks sent.
func sendProcessingRequestBodyStreamed(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, body []byte, chunkSize int) int {
	t.Helper()
	messagesCount := 0

	for i := 0; i < len(body); i += chunkSize {
		end := i + chunkSize
		if end > len(body) {
			end = len(body)
		}

		err := stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_RequestBody{
				RequestBody: &envoyextproc.HttpBody{
					Body: body[i:end],
				},
			},
		})
		require.NoError(t, err)
		messagesCount++
	}

	// Send a chunk of 0 bytes with EndOfStream set to true to indicate the end of the body
	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestBody{
			RequestBody: &envoyextproc.HttpBody{
				Body:        []byte{},
				EndOfStream: true,
			},
		},
	})
	require.NoError(t, err)
	messagesCount++

	return messagesCount
}

func sendProcessingRequestTrailers(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, trailers map[string]string) {
	t.Helper()

	trailerHeaders := &v3.HeaderMap{}
	for k, v := range trailers {
		trailerHeaders.Headers = append(trailerHeaders.Headers, &v3.HeaderValue{Key: k, RawValue: []byte(v)})
	}

	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestTrailers{
			RequestTrailers: &envoyextproc.HttpTrailers{
				Trailers: trailerHeaders,
			},
		},
	})
	require.NoError(t, err)
}

func sendProcessingResponseHeaders(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, headers map[string]string, status string, hasBody bool) {
	t.Helper()
	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &envoyextproc.HttpHeaders{
				Headers:     makeResponseHeaders(t, headers, status),
				EndOfStream: !hasBody,
			},
		},
	})
	require.NoError(t, err)
}

// sendProcessingResponseBodyStreamed sends the response body in chunks to the stream.
// Returns the total number of message chunks sent.
func sendProcessingResponseBodyStreamed(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, body []byte, chunkSize int) int {
	t.Helper()
	messagesCount := 0

	for i := 0; i < len(body); i += chunkSize {
		end := i + chunkSize
		if end > len(body) {
			end = len(body)
		}

		err := stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_ResponseBody{
				ResponseBody: &envoyextproc.HttpBody{
					Body: body[i:end],
				},
			},
		})
		require.NoError(t, err)
		messagesCount++
	}

	// Send a chunk of 0 bytes with EndOfStream set to true to indicate the end of the body
	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_ResponseBody{
			ResponseBody: &envoyextproc.HttpBody{
				Body:        []byte{},
				EndOfStream: true,
			},
		},
	})
	require.NoError(t, err)
	messagesCount++

	return messagesCount
}

func end2EndStreamRequest(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, path string, method string, requestHeaders map[string]string, responseHeaders map[string]string, blockOnResponseHeaders bool, blockOnResponseBody bool, requestBody string, responseBody string) {
	t.Helper()

	// First part: request
	// 1- Send the headers
	sendProcessingRequestHeaders(t, stream, requestHeaders, method, path, len(requestBody) != 0)

	res, err := stream.Recv()
	require.NoError(t, err)
	require.IsType(t, &envoyextproc.ProcessingResponse_RequestHeaders{}, res.GetResponse())
	require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetRequestHeaders().GetResponse().GetStatus())

	if res.GetModeOverride().GetRequestBodyMode() == envoyextprocfilter.ProcessingMode_STREAMED {
		// 2- Send the body
		msgRequestBodySent := sendProcessingRequestBodyStreamed(t, stream, []byte(requestBody), 1)

		for i := 0; i < msgRequestBodySent; i++ {
			res, err = stream.Recv()
			require.NoError(t, err)
			require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetRequestBody().GetResponse().GetStatus())
		}
	}

	// 3- Send the trailers
	sendProcessingRequestTrailers(t, stream, map[string]string{"key": "value"})

	res, err = stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, res.GetRequestTrailers())

	// Second part: response
	// 1- Send the response headers
	sendProcessingResponseHeaders(t, stream, responseHeaders, "200", len(responseBody) != 0)

	if blockOnResponseHeaders || res.GetModeOverride().GetResponseBodyMode() != envoyextprocfilter.ProcessingMode_STREAMED {
		// Should have received an immediate response for blocking or the body wasn't accepted for analysis
		// Let the test handle the response
		return
	}

	res, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetResponseHeaders().GetResponse().GetStatus())

	// 2- Send the response body
	msgResponseBodySent := sendProcessingResponseBodyStreamed(t, stream, []byte(responseBody), 1)

	// minus 1 because the last message is the end of stream, and the connection will be closed after that
	// because no appsec event will be found
	for i := 0; i < msgResponseBodySent-1; i++ {
		res, err = stream.Recv()
		require.NoError(t, err)
		require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetResponseBody().GetResponse().GetStatus())
	}

	if blockOnResponseBody {
		return
	}

	// The stream should now be closed
	_, err = stream.Recv()
	require.Equal(t, io.EOF, err)
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

// Construct request headers
func makeRequestHeaders(t *testing.T, headers map[string]string, method string, path string) *v3.HeaderMap {
	t.Helper()

	h := &v3.HeaderMap{}
	for k, v := range headers {
		h.Headers = append(h.Headers, &v3.HeaderValue{Key: k, RawValue: []byte(v)})
	}

	h.Headers = append(h.Headers,
		&v3.HeaderValue{Key: ":method", RawValue: []byte(method)},
		&v3.HeaderValue{Key: ":path", RawValue: []byte(path)},
		&v3.HeaderValue{Key: ":scheme", RawValue: []byte("https")},
		&v3.HeaderValue{Key: ":authority", RawValue: []byte("datadoghq.com")},
	)

	return h
}

func makeResponseHeaders(t *testing.T, headers map[string]string, status string) *v3.HeaderMap {
	t.Helper()

	h := &v3.HeaderMap{}
	for k, v := range headers {
		h.Headers = append(h.Headers, &v3.HeaderValue{Key: k, RawValue: []byte(v)})
	}

	h.Headers = append(h.Headers, &v3.HeaderValue{Key: ":status", RawValue: []byte(status)})

	return h
}
