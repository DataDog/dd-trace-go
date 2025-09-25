// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package go_control_plane

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	envoyextprocfilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

		// Check for trace context
		require.Equal(t, uint64(12345), span.Context().TraceID())
		require.Equal(t, uint64(67890), span.ParentID())
	})
}

func newEnvoyAppsecRig(t *testing.T, blockingUnavailable bool) (*envoyAppsecRig, error) {
	t.Helper()

	server := grpc.NewServer()
	fixtureServer := new(envoyFixtureServer)

	if blockingUnavailable {
		_ = os.Setenv("_DD_APPSEC_BLOCKING_UNAVAILABLE", "true")
	}

	var appsecSrv envoyextproc.ExternalProcessorServer
	appsecSrv = AppsecEnvoyExternalProcessorServer(fixtureServer)

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
