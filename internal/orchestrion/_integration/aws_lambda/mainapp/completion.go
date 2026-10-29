// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
)

type streamingResponse struct {
	ctx  context.Context
	read bool
	fail bool
}

func (r *streamingResponse) Read(p []byte) (int, error) {
	if r.read {
		if r.fail {
			return 0, errors.New("late stream failure")
		}
		return 0, io.EOF
	}
	r.read = true
	url := os.Getenv("TEST_SERVER_URL") //nolint:forbidigo // test setting, not a DD_* configuration
	// The Runtime API consumes the response on a transport goroutine. Carry
	// the invocation context explicitly across that goroutine boundary.
	if err := get(r.ctx, url+"/during-read"); err != nil {
		return 0, err
	}
	ddlambda.Metric("stream.metric", 1)
	return copy(p, "stream response"), nil
}

func (*streamingResponse) Close() error        { fmt.Println("response closed"); return nil }
func (*streamingResponse) ContentType() string { return "application/x-lambda-test" }

func startStreaming(fail bool) {
	lambda.Start(func(ctx context.Context) (io.Reader, error) {
		return &streamingResponse{ctx: ctx, fail: fail}, nil
	})
}

func panicHandler(context.Context) error { panic("handler panic") }

func startPanic(manual bool) {
	if manual {
		lambda.Start(ddlambda.WrapFunction(panicHandler, nil))
		return
	}
	lambda.Start(panicHandler)
}
