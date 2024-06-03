// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (FixtureClient, mocktracer.Tracer, func()) {
		rig, err := newAppsecRig(false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("unary", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		res, err := client.Ping(ctx, &FixtureRequest{Name: "<script>window.location;</script>"})
		// Check that the handler was properly called
		require.NoError(t, err)
		require.Equal(t, "passed", res.Message)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The request should have the attack attempts
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.Contains(t, event, "crs-941-180") // XSS attack attempt
		require.Contains(t, event, "ua0-600-55x") // canary rule attack attempt
	})

	t.Run("stream", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		// Send a XSS attack in the payload along with the canary value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "dd-test-scanner-log"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send a XSS attack
		err = stream.Send(&FixtureRequest{Name: "<script>window.location;</script>"})
		require.NoError(t, err)

		// Check that the handler was properly called
		res, err := stream.Recv()
		require.Equal(t, "passed", res.Message)
		require.NoError(t, err)

		for i := 0; i < 5; i++ { // Fire multiple times, each time should result in a detected event
			// Send a SQLi attack
			err = stream.Send(&FixtureRequest{Name: fmt.Sprintf("-%[1]d' and %[1]d=%[1]d union select * from users--", i)})
			require.NoError(t, err)

			// Check that the handler was properly called
			res, err = stream.Recv()
			require.Equal(t, "passed", res.Message)
			require.NoError(t, err)
		}

		err = stream.CloseSend()
		require.NoError(t, err)
		// to flush the spans
		stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 14)

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
		err = json.Unmarshal([]byte(jsonText), &parsed)
		require.NoError(t, err)

		histogram := map[string]uint8{}
		for _, tr := range parsed.Triggers {
			histogram[tr.Rule.ID]++
		}

		require.EqualValues(t, 1, histogram["crs-941-180"]) // XSS attack attempt
		require.EqualValues(t, 5, histogram["crs-942-270"]) // SQL-injection attack attempt
		require.EqualValues(t, 1, histogram["ua0-600-55x"]) // canary rule attack attempt

		require.Len(t, histogram, 3)
	})
}

// Test that http blocking works by using custom rules/rules data
func TestBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (FixtureClient, mocktracer.Tracer, func()) {
		rig, err := newAppsecRig(false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("unary-block", func(t *testing.T) {
		for _, tc := range []struct {
			name                    string
			md                      metadata.MD
			message                 string
			expectedBlocked         bool
			expectedMatchedRules    []string
			expectedNotMatchedRules []string
		}{
			{
				name:                    "ip blocking",
				md:                      metadata.Pairs("m1", "v1", "x-client-ip", "1.2.3.4", "user-id", "blocked-user-1"),
				message:                 "$globals",
				expectedMatchedRules:    []string{"blk-001-001"},                      // ip blocking alone as it comes first
				expectedNotMatchedRules: []string{"crs-933-130-block", "blk-001-002"}, // no user blocking or message blocking
			},
			{
				name:                    "message blocking",
				md:                      metadata.Pairs("m1", "v1", "x-client-ip", "1.2.3.5", "user-id", "legit-user-1"),
				message:                 "$globals",
				expectedMatchedRules:    []string{"crs-933-130-block"}, // message blocking alone as it comes before user blocking
				expectedNotMatchedRules: []string{"blk-001-002"},       // no user blocking
			},
			{
				name:                    "user blocking",
				md:                      metadata.Pairs("m1", "v1", "x-client-ip", "1.2.3.5", "user-id", "blocked-user-1"),
				message:                 "<script>alert('xss');</script>",
				expectedMatchedRules:    []string{"blk-001-002"},       // user blocking alone as it comes first in our test handler
				expectedNotMatchedRules: []string{"crs-933-130-block"}, // message blocking alone as it comes before user blocking
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// Helper assertion function to run for the unary and stream tests
				assert := func(t *testing.T, do func(client FixtureClient)) {
					client, mt, cleanup := setup()
					defer cleanup()

					do(client)

					finished := mt.FinishedSpans()
					require.True(t, len(finished) >= 1) // streaming RPCs will have two spans, unary RPCs will have one

					// The request should have the security events
					events, _ := finished[len(finished)-1 /* root span */].Tag("_dd.appsec.json").(string)
					require.NotEmpty(t, events)
					for _, rule := range tc.expectedMatchedRules {
						require.Contains(t, events, rule)
					}
					for _, rule := range tc.expectedNotMatchedRules {
						require.NotContains(t, events, rule)
					}
				}

				t.Run("unary", func(t *testing.T) {
					assert(t, func(client FixtureClient) {
						ctx := metadata.NewOutgoingContext(context.Background(), tc.md)
						reply, err := client.Ping(ctx, &FixtureRequest{Name: tc.message})
						require.Nil(t, reply)
						require.Equal(t, codes.Aborted, status.Code(err))
					})
				})

				t.Run("stream", func(t *testing.T) {
					assert(t, func(client FixtureClient) {
						ctx := metadata.NewOutgoingContext(context.Background(), tc.md)

						// Open the stream
						stream, err := client.StreamPing(ctx)
						require.NoError(t, err)
						defer func() {
							require.NoError(t, stream.CloseSend())
						}()

						// Send a message
						err = stream.Send(&FixtureRequest{Name: tc.message})
						require.NoError(t, err)

						// Receive a message
						reply, err := stream.Recv()
						require.Equal(t, codes.Aborted, status.Code(err))
						require.Nil(t, reply)
					})
				})
			})
		}
	})
}

func TestPasslist(t *testing.T) {
	// This custom rule file includes two rules detecting the same sec event, a grpc metadata value containing "zouzou",
	// but only one of them is passlisted (custom-1 is passlisted, custom-2 is not and must trigger).
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/passlist.json")

	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	setup := func() (FixtureClient, mocktracer.Tracer, func()) {
		rig, err := newAppsecRig(false)
		require.NoError(t, err)

		mt := mocktracer.Start()

		return rig.client, mt, func() {
			rig.Close()
			mt.Stop()
		}
	}

	t.Run("unary", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		// Send the payload triggering the sec event thanks to the "zouzou" value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "zouzou"))
		res, err := client.Ping(ctx, &FixtureRequest{Name: "hello"})

		// Check that the handler was properly called
		require.NoError(t, err)
		require.Equal(t, "passed", res.Message)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The service entry span must include the sec event
		event, _ := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.NotContains(t, event, "custom-1") // custom-1 is in the passlist of this gRPC method
		require.Contains(t, event, "custom-2")    // custom-2 is not passlisted and must trigger an event
	})

	t.Run("stream", func(t *testing.T) {
		client, mt, cleanup := setup()
		defer cleanup()

		// Open the steam triggering the sec event thanks to the "zouzou" value in the RPC metadata
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("dd-canary", "zouzou"))
		stream, err := client.StreamPing(ctx)
		require.NoError(t, err)

		// Send some messages
		for i := 0; i < 5; i++ {
			err = stream.Send(&FixtureRequest{Name: "hello"})
			require.NoError(t, err)

			// Check that the handler was properly called
			res, err := stream.Recv()
			require.Equal(t, "passed", res.Message)
			require.NoError(t, err)
		}

		err = stream.CloseSend()
		require.NoError(t, err)
		// Flush the spans
		stream.Recv()

		finished := mt.FinishedSpans()
		require.Len(t, finished, 12)

		// The service entry span must include the sec event
		event := finished[len(finished)-1].Tag("_dd.appsec.json")
		require.NotNil(t, event, "the _dd.appsec.json tag was not found")
		require.NotContains(t, event, "custom-1") // custom-1 is in the passlist of this gRPC method
		require.Contains(t, event, "custom-2")    // custom-2 is not passlisted and must trigger an event
	})
}

func newAppsecRig(traceClient bool, interceptorOpts ...Option) (*appsecRig, error) {
	interceptorOpts = append([]InterceptorOption{WithServiceName("grpc")}, interceptorOpts...)

	server := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryServerInterceptor(interceptorOpts...)),
		grpc.StreamInterceptor(StreamServerInterceptor(interceptorOpts...)),
	)

	fixtureServer := new(appsecFixtureServer)
	RegisterFixtureServer(server, fixtureServer)

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
			grpc.WithUnaryInterceptor(UnaryClientInterceptor(interceptorOpts...)),
			grpc.WithStreamInterceptor(StreamClientInterceptor(interceptorOpts...)),
		)
	}
	conn, err := grpc.Dial(li.Addr().String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &appsecRig{
		fixtureServer: fixtureServer,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        NewFixtureClient(conn),
	}, err
}

// rig contains all of the servers and connections we'd need for a
// grpc integration test
type appsecRig struct {
	fixtureServer *appsecFixtureServer
	server        *grpc.Server
	port          string
	listener      net.Listener
	conn          *grpc.ClientConn
	client        FixtureClient
}

func (r *appsecRig) Close() {
	r.server.Stop()
	r.conn.Close()
}

type appsecFixtureServer struct {
	UnimplementedFixtureServer
	s fixtureServer
}

func (s *appsecFixtureServer) StreamPing(stream Fixture_StreamPingServer) (err error) {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get("user-id")
	if len(ids) > 0 {
		if err := pappsec.SetUser(ctx, ids[0]); err != nil {
			return err
		}
	}
	return s.s.StreamPing(stream)
}
func (s *appsecFixtureServer) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get("user-id")
	if len(ids) > 0 {
		if err := pappsec.SetUser(ctx, ids[0]); err != nil {
			return nil, err
		}
	}
	return s.s.Ping(ctx, in)
}
