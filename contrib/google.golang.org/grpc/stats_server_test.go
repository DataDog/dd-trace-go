// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestServerStatsHandler(t *testing.T) {
	assert := assert.New(t)

	serviceName := "grpc-service"
	statsHandler := NewServerStatsHandler(WithServiceName(serviceName))
	server, err := newServerStatsHandlerTestServer(statsHandler)
	if err != nil {
		t.Fatalf("failed to start test server: %s", err)
	}
	defer server.Close()

	mt := mocktracer.Start()
	defer mt.Stop()
	_, err = server.client.Ping(context.Background(), &FixtureRequest{Name: "name"})
	assert.NoError(err)

	spans := mt.FinishedSpans()
	if len(spans) == 0 {
		// we fetched finished spans too soon
		spans, err = waitOnSpans(mt.FinishedSpans, 1*time.Second)
		assert.NoError(err)
	}
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
}

func newServerStatsHandlerTestServer(statsHandler stats.Handler) (*rig, error) {
	server := grpc.NewServer(grpc.StatsHandler(statsHandler))
	fixtureServer := new(fixtureServer)
	RegisterFixtureServer(server, fixtureServer)

	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(li.Addr().String())
	go server.Serve(li)

	conn, err := grpc.Dial(li.Addr().String(), grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("error dialing: %s", err)
	}
	return &rig{
		fixtureServer: fixtureServer,
		listener:      li,
		port:          port,
		server:        server,
		conn:          conn,
		client:        NewFixtureClient(conn),
	}, nil
}

func waitOnSpans(fetch func() []mocktracer.Span, timeout time.Duration) ([]mocktracer.Span, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("couldn't get spans in time")
		case <-ticker.C:
			if spans := fetch(); len(spans) > 0 {
				return spans, nil
			}
		}
	}
}
