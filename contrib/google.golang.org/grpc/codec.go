// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"

	"google.golang.org/grpc/encoding"
	encodingproto "google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/mem"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

func init() {
	if !internal.BoolEnv("DD_TRACE_GRPC_TRACE_ENCODING", false) {
		return
	}
	_ = registerCodec()
}

func registerCodec() (unregister func()) {
	protoCodec := encoding.GetCodecV2(encodingproto.Name)
	encoding.RegisterCodecV2(&tracedCodec{protoCodec})
	return func() {
		encoding.RegisterCodecV2(protoCodec)
	}
}

type tracedCodec struct {
	protoCodec encoding.CodecV2
}

func (t *tracedCodec) Marshal(v any) (out mem.BufferSlice, err error) {
	ctx := context.Background()
	span, _ := tracer.StartSpanFromContext(ctx, "proto.Marshal")
	defer span.Finish(tracer.WithError(err))
	return t.protoCodec.Marshal(v)
}

func (t *tracedCodec) Unmarshal(data mem.BufferSlice, v any) (err error) {
	ctx := context.Background()
	span, _ := tracer.StartSpanFromContext(ctx, "proto.Unmarshal")
	defer span.Finish(tracer.WithError(err))
	return t.protoCodec.Unmarshal(data, v)
}

func (t *tracedCodec) Name() string {
	return encodingproto.Name
}
