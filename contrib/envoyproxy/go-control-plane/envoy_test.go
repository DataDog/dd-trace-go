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

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoytypes "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	ddgrpc "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestAppSec(t *testing.T) {
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("monitoring-event-on-request", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "dd-test-scanner-log"}, map[string]string{}, false)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})
	})

	t.Run("blocking-event-on-request", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		err = stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_RequestHeaders{
				RequestHeaders: &envoyextproc.HttpHeaders{
					Headers: makeRequestHeaders(t, map[string]string{"User-Agent": "dd-test-scanner-log-block"}, "GET", "/"),
				},
			},
		})
		require.NoError(t, err)

		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-56x": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
}

func TestBlockingWithUserRulesFile(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("blocking-event-on-response", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "OPTION", map[string]string{"User-Agent": "dd-test-scanner-log-block"}, map[string]string{"User-Agent": "match-response-headers"}, true)

		// Handle the immediate response
		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code) // 418 because of the rule file
		require.Len(t, res.GetImmediateResponse().GetHeaders().SetHeaders, 1)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"headers-003": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})

	t.Run("blocking-event-on-request-on-query", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		err = stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_RequestHeaders{
				RequestHeaders: &envoyextproc.HttpHeaders{
					Headers: makeRequestHeaders(t, map[string]string{"User-Agent": "Mistake Not..."}, "GET", "/hello?match=match-request-query"),
				},
			},
		})
		require.NoError(t, err)

		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

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

		err = stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_RequestHeaders{
				RequestHeaders: &envoyextproc.HttpHeaders{
					Headers: makeRequestHeaders(t, map[string]string{"Cookie": "foo=jdfoSDGFkivRG_234"}, "OPTIONS", "/"),
				},
			},
		})
		require.NoError(t, err)

		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(418), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"tst-037-008": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
}

func TestGeneratedSpan(t *testing.T) {
	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false)
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

		end2EndStreamRequest(t, stream, "/../../../resource-span/.?id=test", "GET", map[string]string{"user-agent": "Mistake Not...", "test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

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

		end2EndStreamRequest(t, stream, "/../../../resource-span/.?id=test", "GET", map[string]string{"user-agent": "Mistake Not...", "test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

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

func TestXForwardedForHeaderClientIp(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("client-ip", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		ctx := context.Background()
		stream, err := client.Process(ctx)
		require.NoError(t, err)

		end2EndStreamRequest(t, stream, "/", "OPTION",
			map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "18.18.18.18"},
			map[string]string{"User-Agent": "match-response-headers"},
			true)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

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

		err = stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_RequestHeaders{
				RequestHeaders: &envoyextproc.HttpHeaders{
					Headers: makeRequestHeaders(t, map[string]string{"User-Agent": "Mistake not...", "X-Forwarded-For": "1.2.3.4"}, "GET", "/"),
				},
			},
		})
		require.NoError(t, err)

		// Handle the immediate response
		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, envoytypes.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
		require.Equal(t, "Content-Type", res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().Key)
		require.Equal(t, "application/json", string(res.GetImmediateResponse().GetHeaders().SetHeaders[0].GetHeader().RawValue))
		require.NoError(t, err)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"blk-001-001": 1})

		// Check for tags
		span := finished[0]
		require.Equal(t, "1.2.3.4", span.Tag("http.client_ip"))
		require.Equal(t, 1.0, span.Tag("_dd.appsec.enabled"))
		require.Equal(t, "true", span.Tag("appsec.event"))
		require.Equal(t, "true", span.Tag("appsec.blocked"))
	})
}

func TestMalformedEnvoyProcessing(t *testing.T) {
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false, false, false)
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

		err = stream.Send(&envoyextproc.ProcessingRequest{
			Request: &envoyextproc.ProcessingRequest_ResponseHeaders{
				ResponseHeaders: &envoyextproc.HttpHeaders{
					Headers: makeResponseHeaders(t, map[string]string{}, "400"),
				},
			},
		})
		require.NoError(t, err)

		_, err = stream.Recv()
		require.Error(t, err)
		stream.Recv()

		// No span created, the request is invalid.
		// Span couldn't be created without request data
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
		rig, err := newEnvoyAppsecRig(t, false, true, false)
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

		end2EndStreamRequest(t, stream, "/", "GET", map[string]string{"User-Agent": "dd-test-scanner-log"}, map[string]string{}, false)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		checkForAppsecEvent(t, finished, map[string]int{"ua0-600-55x": 1})

		// Check for component tag
		span := finished[0]
		require.Equal(t, "gcp-service-extension", span.Tag("component"))
	})
}

func newEnvoyAppsecRig(t *testing.T, traceClient bool, isGCPServiceExtension bool, blockingUnavailable bool, interceptorOpts ...ddgrpc.Option) (*envoyAppsecRig, error) {
	t.Helper()

	interceptorOpts = append([]ddgrpc.Option{ddgrpc.WithService("grpc")}, interceptorOpts...)

	server := grpc.NewServer()
	fixtureServer := new(envoyFixtureServer)

	if blockingUnavailable {
		_ = os.Setenv("_DD_APPSEC_BLOCKING_UNAVAILABLE", "true")
	}

	var appsecSrv envoyextproc.ExternalProcessorServer
	appsecSrv = AppsecEnvoyExternalProcessorServer(fixtureServer, AppsecEnvoyConfig{
		IsGCPServiceExtension: isGCPServiceExtension,
		BlockingUnavailable:   blockingUnavailable,
	})

	envoyextproc.RegisterExternalProcessorServer(server, appsecSrv)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	// start our test fixtureServer.
	go server.Serve(li)

	opts := []grpc.DialOption{grpc.WithInsecure()}
	if traceClient {
		opts = append(opts,
			grpc.WithStreamInterceptor(ddgrpc.StreamClientInterceptor(interceptorOpts...)),
		)
	}
	conn, err := grpc.Dial(li.Addr().String(), opts...)
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
	r.conn.Close()
}

type envoyFixtureServer struct {
	envoyextproc.ExternalProcessorServer
}

// Helper functions

func end2EndStreamRequest(t *testing.T, stream envoyextproc.ExternalProcessor_ProcessClient, path string, method string, requestHeaders map[string]string, responseHeaders map[string]string, blockOnResponse bool) {
	t.Helper()

	// First part: request
	// 1- Send the headers
	err := stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestHeaders{
			RequestHeaders: &envoyextproc.HttpHeaders{
				Headers: makeRequestHeaders(t, requestHeaders, method, path),
			},
		},
	})
	require.NoError(t, err)

	res, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetRequestHeaders().GetResponse().GetStatus())

	// 2- Send the body
	err = stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestBody{
			RequestBody: &envoyextproc.HttpBody{
				Body: []byte("body"),
			},
		},
	})
	require.NoError(t, err)

	res, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetRequestBody().GetResponse().GetStatus())

	// 3- Send the trailers
	err = stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_RequestTrailers{
			RequestTrailers: &envoyextproc.HttpTrailers{
				Trailers: &v3.HeaderMap{
					Headers: []*v3.HeaderValue{
						{Key: "key", Value: "value"},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	res, err = stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, res.GetRequestTrailers())

	// Second part: response
	// 1- Send the response headers
	err = stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &envoyextproc.HttpHeaders{
				Headers: makeResponseHeaders(t, responseHeaders, "200"),
			},
		},
	})
	require.NoError(t, err)

	if blockOnResponse {
		// Should have received an immediate response for blocking
		// Let the test handle the response
		return
	}

	res, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, envoyextproc.CommonResponse_CONTINUE, res.GetResponseHeaders().GetResponse().GetStatus())

	// 2- Send the response body
	err = stream.Send(&envoyextproc.ProcessingRequest{
		Request: &envoyextproc.ProcessingRequest_ResponseBody{
			ResponseBody: &envoyextproc.HttpBody{
				Body:        []byte("body"),
				EndOfStream: true,
			},
		},
	})
	require.NoError(t, err)

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
		require.Equal(t, count, int(histogram[ruleID]), "rule %s has been triggered %d times but expected %d")
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
