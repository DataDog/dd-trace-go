// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:generate sh ./gen_proto.sh .

package fixturepb

import (
	"context"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// FixtureSrv a dummy implementation of FixtureServer.
type FixtureSrv struct {
	UnimplementedFixtureServer
	LastRequestMetadata atomic.Value
}

func NewFixtureServer() *FixtureSrv {
	return &FixtureSrv{}
}

func (s *FixtureSrv) StreamPing(stream Fixture_StreamPingServer) (err error) {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		reply, err := s.Ping(stream.Context(), msg)
		if err != nil {
			return err
		}

		err = stream.Send(reply)
		if err != nil {
			return err
		}

		if msg.Name == "break" {
			return nil
		}
	}
}

func (s *FixtureSrv) Ping(ctx context.Context, in *FixtureRequest) (*FixtureReply, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		s.LastRequestMetadata.Store(md)
	}
	switch {
	case in.Name == "child":
		span, _ := tracer.StartSpanFromContext(ctx, "child")
		span.Finish()
		return &FixtureReply{Message: "child"}, nil
	case in.Name == "disabled":
		if _, ok := tracer.SpanFromContext(ctx); ok {
			panic("should be disabled")
		}
		return &FixtureReply{Message: "disabled"}, nil
	case in.Name == "invalid":
		return nil, status.Error(codes.InvalidArgument, "invalid")
	case in.Name == "errorDetails":
		s, _ := status.New(codes.Unknown, "unknown").
			WithDetails(&FixtureReply{Message: "a"}, &FixtureReply{Message: "b"})
		return nil, s.Err()
	}
	return &FixtureReply{Message: "passed"}, nil
}
