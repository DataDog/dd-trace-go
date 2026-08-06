// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

func TestServerStatsHandler(t *testing.T) {
	assert := assert.New(t)

	serviceName := "grpc-service"
	statsHandler := NewServerStatsHandler(WithService(serviceName), WithSpanOptions(tracer.Tag("foo", "bar")))
	server, err := newServerStatsHandlerTestServer(statsHandler)
	if err != nil {
		t.Fatalf("failed to start test server: %s", err)
	}
	defer server.Close()

	mt := mocktracer.Start()
	defer mt.Stop()
	_, err = server.client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "name"})
	assert.NoError(err)

	waitForSpans(mt, 1)
	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Zero(span.ParentID())
	assert.NotZero(span.StartTime())
	assert.True(span.FinishTime().Sub(span.StartTime()) >= 0)
	assert.Equal("grpc.server", span.OperationName())
	tags := span.Tags()
	assert.Equal(ext.AppTypeRPC, tags["span.type"])
	assert.Equal(codes.OK.String(), tags["grpc.code"])
	assert.Equal(serviceName, tags["service.name"])
	assert.Equal("/grpc.Fixture/Ping", tags["resource.name"])
	assert.Equal("/grpc.Fixture/Ping", tags[tagMethodName])
	assert.Equal("bar", tags["foo"])
	assert.Equal("grpc", tags[ext.RPCSystem])
	assert.Equal("/grpc.Fixture/Ping", tags[ext.GRPCFullMethod])
	assert.Equal(ext.SpanKindServer, tags[ext.SpanKind])
	assert.Equal(instrumentation.ServiceSourceWithServiceOption, tags[ext.KeyServiceSource])
}

// TestServerStatsHandlerWithErrorCheck verifies that WithErrorCheck is invoked with
// the correct full method on the stats-handler path, and that a suppressed error does
// not mark the span as errored. The errCheck only matches on the expected method, so a
// suppressed error tag also proves the method was propagated correctly through the context.
func TestServerStatsHandlerWithErrorCheck(t *testing.T) {
	for name, tt := range map[string]struct {
		errCheck func(method string, err error) bool
		wantErr  bool
	}{
		"treat matching method as non-error": {
			errCheck: func(method string, err error) bool {
				if method == "/grpc.Fixture/Ping" && status.Code(err) == codes.InvalidArgument {
					return false
				}
				return true
			},
			wantErr: false,
		},
		"treat other errors as errors": {
			errCheck: func(method string, err error) bool {
				if method == "/other/Method" && status.Code(err) == codes.InvalidArgument {
					return false
				}
				return true
			},
			wantErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)

			statsHandler := NewServerStatsHandler(WithErrorCheck(tt.errCheck))
			server, err := newServerStatsHandlerTestServer(statsHandler)
			if err != nil {
				t.Fatalf("failed to start test server: %s", err)
			}
			defer server.Close()

			mt := mocktracer.Start()
			defer mt.Stop()

			_, err = server.client.Ping(context.Background(), &fixturepb.FixtureRequest{Name: "invalid"})
			assert.Error(err)

			waitForSpans(mt, 1)
			spans := mt.FinishedSpans()
			assert.Len(spans, 1)

			span := spans[0]
			assert.Equal(codes.InvalidArgument.String(), span.Tag(tagCode))
			if tt.wantErr {
				assert.NotNil(span.Tag(ext.ErrorMsg))
			} else {
				assert.Nil(span.Tag(ext.ErrorMsg))
			}
		})
	}
}

func newServerStatsHandlerTestServer(statsHandler stats.Handler) (*rig, error) {
	return newRigWithInterceptors(
		[]grpc.ServerOption{
			grpc.StatsHandler(statsHandler),
		},
		[]grpc.DialOption{
			grpc.WithInsecure(),
		},
	)
}
