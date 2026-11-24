// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp is an AWS Lambda function built with orchestrion. The
// LAMBDA_VARIANT environment variable selects how it starts its handler and
// what the handler does. Every handler tags the active span with
// lambda_variant and makes one GET to LAMBDA_HTTP_URL, so each invocation
// should report an aws.lambda span with one http.request child.
//
// The default build doesn't import ddlambda, as a customer's function
// wouldn't, so the tests show that Orchestrion links it in on its own.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var (
	variant = os.Getenv("LAMBDA_VARIANT")  //nolint:forbidigo // set by the test, not a tracer setting
	httpURL = os.Getenv("LAMBDA_HTTP_URL") //nolint:forbidigo // set by the test, not a tracer setting
)

type ctxKey struct{}

type structHandler struct{}

func (structHandler) Invoke(ctx context.Context, _ []byte) ([]byte, error) {
	markInvocation(ctx)
	return []byte("handler-ok"), nil
}

type ctxHandler struct{}

func (ctxHandler) Invoke(ctx context.Context, _ []byte) ([]byte, error) {
	markInvocation(ctx)
	v, _ := ctx.Value(ctxKey{}).(string)
	return []byte(v), nil
}

// httpClient does not follow redirects, so each invocation makes one request.
var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// markInvocation tags the active span with the variant and makes the
// downstream HTTP request.
func markInvocation(ctx context.Context) {
	if span, ok := tracer.SpanFromContext(ctx); ok {
		span.SetTag("lambda_variant", variant)
	}

	url := httpURL
	if url == "" {
		url = "https://www.datadoghq.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("downstream request: %s", err.Error())
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("downstream request: %s", err.Error())
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

func ok(ctx context.Context) (string, error) {
	markInvocation(ctx)
	return "ok", nil
}

func fromContext(ctx context.Context) (string, error) {
	markInvocation(ctx)
	v, _ := ctx.Value(ctxKey{}).(string)
	return v, nil
}

func main() {
	base := context.WithValue(context.Background(), ctxKey{}, "from-context")

	switch variant {
	case "Start":
		lambda.Start(ok)
	case "StartWithContext":
		lambda.StartWithContext(base, fromContext) //nolint:staticcheck // deprecated entry points are woven too
	case "StartHandler":
		lambda.StartHandler(structHandler{}) //nolint:staticcheck // deprecated entry points are woven too
	case "StartHandlerWithContext":
		lambda.StartHandlerWithContext(base, ctxHandler{}) //nolint:staticcheck // deprecated entry points are woven too
	case "StartHandlerFunc":
		lambda.StartHandlerFunc(func(ctx context.Context, _ map[string]any) (string, error) {
			return ok(ctx)
		})
	case "NewHandler":
		lambda.Start(lambda.NewHandler(ok))
	case "UseNumber":
		lambda.StartWithOptions(func(ctx context.Context, event map[string]any) (string, error) {
			markInvocation(ctx)
			return fmt.Sprintf("%T", event["n"]), nil
		}, lambda.WithUseNumber(true))
	case "WithContext":
		lambda.StartWithOptions(fromContext, lambda.WithContext(base))
	case "DisallowUnknownFields":
		lambda.StartWithOptions(func(ctx context.Context, _ struct{ Name string }) (string, error) {
			markInvocation(ctx)
			return "ok", nil
		}, lambda.WithDisallowUnknownFields(true))
	case "ResponseFormat":
		lambda.StartWithOptions(func(ctx context.Context) (map[string]string, error) {
			markInvocation(ctx)
			return map[string]string{"html": "<b>"}, nil
		}, lambda.WithSetEscapeHTML(true), lambda.WithSetIndent("", " "))
	case "Stream":
		lambda.Start(func(ctx context.Context) (io.Reader, error) {
			markInvocation(ctx)
			return strings.NewReader("streamed-body"), nil
		})
	case "Error":
		lambda.Start(func(ctx context.Context) (string, error) {
			markInvocation(ctx)
			return "", errors.New("boom")
		})
	case "DecodeError":
		lambda.Start(func(ctx context.Context, _ struct{ N int }) (string, error) {
			markInvocation(ctx)
			return "ok", nil
		})
	case "ErrorThenOK":
		calls := 0
		lambda.Start(func(ctx context.Context) (string, error) {
			markInvocation(ctx)
			calls++
			if calls == 1 {
				return "", errors.New("boom")
			}
			return "ok", nil
		})
	default:
		panic("unknown LAMBDA_VARIANT " + variant)
	}
}
