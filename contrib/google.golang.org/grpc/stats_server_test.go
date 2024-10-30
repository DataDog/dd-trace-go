// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
)

func TestServerStatsHandler(t *testing.T) {
	assert := assert.New(t)

	serviceName := "grpc-service"
	statsHandler := NewServerStatsHandler(WithServiceName(serviceName), WithSpanOptions(tracer.Tag("foo", "bar")))
	server, err := newServerStatsHandlerTestServer(statsHandler)
	if err != nil {
		t.Fatalf("failed to start test server: %s", err)
	}
	defer server.Close()

	mt := mocktracer.Start()
	defer mt.Stop()
	_, err = server.client.Ping(context.Background(), &FixtureRequest{Name: "name"})
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
	assert.Equal(1, tags["_dd.measured"])
	assert.Equal("bar", tags["foo"])
	assert.Equal("grpc", tags[ext.RPCSystem])
	assert.Equal("/grpc.Fixture/Ping", tags[ext.GRPCFullMethod])
	assert.Equal(ext.SpanKindServer, tags[ext.SpanKind])
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
