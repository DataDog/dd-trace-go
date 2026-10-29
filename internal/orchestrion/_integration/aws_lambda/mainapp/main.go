// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp is an AWS Lambda function that starts its handler with the
// lambda.Start* form named by the LAMBDA_VARIANT environment variable. Apart
// from the prewrapped variant, it doesn't use ddlambda: Orchestrion adds it.
// TestLambda builds and runs it once per variant against a fake Lambda Runtime API.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
)

type event struct {
	Name string `json:"name"`
}

func handler(ctx context.Context, _ event) (string, error) {
	return "ok", work(ctx)
}

type structHandler struct{}

func (structHandler) Invoke(ctx context.Context, _ []byte) ([]byte, error) {
	return []byte(`"ok"`), work(ctx)
}

// work makes two HTTP calls: one that carries the invocation's context, and
// one that doesn't, whose span can only find its parent through Orchestrion's
// goroutine-local storage.
func work(ctx context.Context) error {
	url := os.Getenv("TEST_SERVER_URL") //nolint:forbidigo // test setting, not a DD_* configuration
	if err := get(ctx, url+"/with-context"); err != nil {
		return err
	}
	return get(context.Background(), url+"/without-context")
}

func get(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func main() {
	ctx := context.Background()
	opts := []lambda.Option{lambda.WithContext(ctx)}
	switch variant := os.Getenv("LAMBDA_VARIANT"); variant { //nolint:forbidigo // test setting, not a DD_* configuration
	case "Streaming":
		startStreaming(false)
	case "StreamingError":
		startStreaming(true)
	case "Panic":
		startPanic(false)
	case "ManualPanic":
		startPanic(true)
	case "Start":
		lambda.Start(handler)
	case "StartWithOptions":
		lambda.StartWithOptions(handler, lambda.WithContext(ctx))
	case "StartWithOptionsSpread":
		lambda.StartWithOptions(handler, opts...)
	case "StartHandlerFunc":
		lambda.StartHandlerFunc(handler)
	case "StartHandlerFuncSpread":
		lambda.StartHandlerFunc(handler, opts...)
	case "ExplicitHandlerFunc":
		lambda.StartHandlerFunc[event, string](handler)
	case "FunctionValue":
		start := lambda.Start
		start(handler)
	case "PrewrappedNewHandler":
		lambda.Start(lambda.NewHandler(ddlambda.WrapFunction(handler, nil)))
	case "PrewrappedInterface":
		lambda.Start(ddlambda.WrapLambdaHandlerInterface(structHandler{}, nil))
	case "UseNumber":
		startUseNumber()
	case "DisallowUnknownFields":
		startDisallowUnknownFields()
	case "PrebuiltContext":
		startPrebuiltContext()
	case "EncodingOptions":
		startEncodingOptions()
	case "StartStruct":
		lambda.Start(structHandler{})
	case "StartNewHandler":
		lambda.Start(lambda.NewHandler(handler))
	case "StartWithContext":
		lambda.StartWithContext(ctx, handler) //nolint:staticcheck // deprecated forms are woven too
	case "StartHandler":
		lambda.StartHandler(structHandler{}) //nolint:staticcheck // deprecated forms are woven too
	case "StartHandlerWithContext":
		lambda.StartHandlerWithContext(ctx, structHandler{}) //nolint:staticcheck // deprecated forms are woven too
	case "Prewrapped":
		lambda.Start(ddlambda.WrapFunction(handler, nil))
	case "Aliased":
		startAliased()
	case "AliasedStartHandlerFunc":
		startHandlerFuncAliased()
	default:
		fmt.Fprintln(os.Stderr, "unknown LAMBDA_VARIANT:", variant)
		os.Exit(2)
	}
}
