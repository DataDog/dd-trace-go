// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package envoy

import (
	"context"
	"encoding/json"
	"fmt"
	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"io"
	"net"
	"testing"

	ddgrpc "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func end2EndStreamRequest(t *testing.T, stream extproc.ExternalProcessor_ProcessClient, path string, method string, requestHeaders map[string]string, responseHeaders map[string]string, blockOnResponse bool) {
	// First part: request
	// 1- Send the headers
	err := stream.Send(&extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extproc.HttpHeaders{
				Headers: makeRequestHeaders(requestHeaders, method, path),
			},
		},
	})
	require.NoError(t, err)

	res, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, extproc.CommonResponse_CONTINUE, res.GetRequestHeaders().GetResponse().GetStatus())

	// 2- Send the body
	err = stream.Send(&extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_RequestBody{
			RequestBody: &extproc.HttpBody{
				Body: []byte("body"),
			},
		},
	})
	require.NoError(t, err)

	res, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, extproc.CommonResponse_CONTINUE, res.GetRequestBody().GetResponse().GetStatus())

	// 3- Send the trailers
	err = stream.Send(&extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_RequestTrailers{
			RequestTrailers: &extproc.HttpTrailers{
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
	err = stream.Send(&extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extproc.HttpHeaders{
				Headers: makeResponseHeaders(responseHeaders, "200"),
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
	require.Equal(t, extproc.CommonResponse_CONTINUE, res.GetResponseHeaders().GetResponse().GetStatus())

	// 2- Send the response body
	err = stream.Send(&extproc.ProcessingRequest{
		Request: &extproc.ProcessingRequest_ResponseBody{
			ResponseBody: &extproc.HttpBody{
				Body: []byte("body"),
			},
		},
	})
	require.NoError(t, err)

	// The stream should now be closed
	_, err = stream.Recv()
	require.Equal(t, io.EOF, err)
}

func checkForAppsecEvent(t *testing.T, finished []mocktracer.Span, expectedRuleIDs map[string]int) {
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

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (extproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(false)
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

		err = stream.Send(&extproc.ProcessingRequest{
			Request: &extproc.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extproc.HttpHeaders{
					Headers: makeRequestHeaders(map[string]string{"User-Agent": "dd-test-scanner-log-block"}, "GET", "/"),
				},
			},
		})
		require.NoError(t, err)

		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, typev3.StatusCode(403), res.GetImmediateResponse().GetStatus().Code)
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
		require.Equal(t, true, span.Tag("appsec.event"))
		require.Equal(t, true, span.Tag("appsec.blocked"))
	})
}

func TestBlockingOnResponse(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/user_rules.json")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (extproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(false)
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

		end2EndStreamRequest(t, stream, "/", "OPTION", map[string]string{"User-Agent": "dd-test-scanner-log-block"}, map[string]string{"User-Agent": "match-response-header"}, true)

		// Handle the immediate response
		res, err := stream.Recv()
		require.Equal(t, uint32(0), res.GetImmediateResponse().GetGrpcStatus().Status)
		require.Equal(t, typev3.StatusCode(418), res.GetImmediateResponse().GetStatus().Code) // 418 because of the rule file
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
		require.Equal(t, true, span.Tag("appsec.event"))
		require.Equal(t, true, span.Tag("appsec.blocked"))
	})
}

func TestGeneratedSpan(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (extproc.ExternalProcessorClient, mocktracer.Tracer, func()) {
		rig, err := newEnvoyAppsecRig(false)
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

		end2EndStreamRequest(t, stream, "/resource-span", "GET", map[string]string{"test-key": "test-value"}, map[string]string{"response-test-key": "response-test-value"}, false)

		err = stream.CloseSend()
		require.NoError(t, err)
		stream.Recv() // to flush the spans

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check for tags
		span := finished[0]
		require.Equal(t, "http.request", span.OperationName())
		require.Equal(t, "https://datadoghq.com/resource-span", span.Tag("http.url"))
		require.Equal(t, "GET", span.Tag("http.method"))
		require.Equal(t, "datadoghq.com", span.Tag("http.host"))
		require.Equal(t, "GET /resource-span", span.Tag("resource.name"))
		require.Equal(t, "datadoghq.com", span.Tag("http.request.headers.host"))
		require.Equal(t, "server", span.Tag("span.kind"))
		require.Equal(t, "web", span.Tag("span.type"))

		// Appsec
		require.Equal(t, 1, span.Tag("_dd.appsec.enabled"))
	})
}

func newEnvoyAppsecRig(traceClient bool, interceptorOpts ...ddgrpc.Option) (*envoyAppsecRig, error) {
	interceptorOpts = append([]ddgrpc.InterceptorOption{ddgrpc.WithServiceName("grpc")}, interceptorOpts...)

	server := grpc.NewServer(
		grpc.StreamInterceptor(StreamServerInterceptor(interceptorOpts...)),
	)

	fixtureServer := new(envoyFixtureServer)
	extproc.RegisterExternalProcessorServer(server, fixtureServer)

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
		client:        extproc.NewExternalProcessorClient(conn),
	}, err
}

// rig contains all servers and connections we'd need for a grpc integration test
type envoyAppsecRig struct {
	fixtureServer *envoyFixtureServer
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        extproc.ExternalProcessorClient
}

func (r *envoyAppsecRig) Close() {
	r.server.Stop()
	r.conn.Close()
}

type envoyFixtureServer struct {
	extproc.ExternalProcessorServer
}

// Helper functions

// Construct request headers
func makeRequestHeaders(headers map[string]string, method string, path string) *v3.HeaderMap {
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

func makeResponseHeaders(headers map[string]string, status string) *v3.HeaderMap {
	h := &v3.HeaderMap{}
	for k, v := range headers {
		h.Headers = append(h.Headers, &v3.HeaderValue{Key: k, RawValue: []byte(v)})
	}

	h.Headers = append(h.Headers, &v3.HeaderValue{Key: ":status", RawValue: []byte(status)})

	return h
}
