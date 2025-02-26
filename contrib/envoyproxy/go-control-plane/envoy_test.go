// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package go_control_plane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	ddgrpc "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestGeneratedSpan(t *testing.T) {
	setup := func() (envoyextproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(t, false)
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

		// Check for trace context
		require.Equal(t, uint64(12345), span.Context().TraceID())
		require.Equal(t, uint64(67890), span.ParentID())
	})
}

func newEnvoyAppsecRig(t *testing.T, traceClient bool, interceptorOpts ...ddgrpc.Option) (*envoyAppsecRig, error) {
	t.Helper()

	interceptorOpts = append([]ddgrpc.InterceptorOption{ddgrpc.WithServiceName("grpc")}, interceptorOpts...)

	server := grpc.NewServer()

	fixtureServer := new(envoyFixtureServer)
	appsecSrv := AppsecEnvoyExternalProcessorServer(fixtureServer)
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

func checkForAppsecEvent(t *testing.T, finished []mocktracer.Span, expectedRuleIDs map[string]int) {
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
