// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package export submits completed OTLP trace, metric, and log requests without
// starting a tracer. A Client targets one Datadog intake or OTLP collector and
// is safe for concurrent use. Each input request is sent atomically as one POST
// and one result; inputs are not merged or split. A Submit call sends its inputs
// sequentially, but callers can issue calls concurrently.
//
// EXPERIMENTAL: This package may change or be removed without notice.
package export

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
)

var errNilRequest = errors.New("otlp/export: nil request")

// Client submits completed OTLP requests to one destination.
type Client struct {
	transport *rawTransport
}

// NewClient creates a client. Exactly one routing option is required.
func NewClient(opts ...ClientOption) (*Client, error) {
	cfg, err := resolveClientConfig(opts)
	if err != nil {
		return nil, err
	}
	return &Client{transport: newRawTransport(cfg)}, nil
}

type partialSuccessFunc func(body []byte) (rejected int64, message string, err error)

func submitEach[T proto.Message](ctx context.Context, transport *rawTransport, path string, requests []T, partial partialSuccessFunc) (*Result, error) {
	result := &Result{}
	for i, request := range requests {
		if err := ctx.Err(); err != nil {
			for ; i < len(requests); i++ {
				result.Requests = append(result.Requests, RequestResult{Index: i, Retriable: true, Err: fmt.Errorf("otlp/export: request not sent: %w", err)})
			}
			break
		}
		if isNilMessage(request) {
			result.Requests = append(result.Requests, RequestResult{Index: i, Err: errNilRequest})
			continue
		}

		requestResult, body := transport.submit(ctx, path, request)
		requestResult.Index = i
		if requestResult.Err == nil {
			rejected, message, err := partial(body)
			diagnostic := ""
			if message != "" {
				diagnostic = responseSnippet([]byte(message))
				requestResult.ResponseSnippet = diagnostic
			}
			switch {
			case err != nil:
				requestResult.ResponseSnippet = responseSnippet(body)
				requestResult.Err = fmt.Errorf("otlp/export: response body is not a valid OTLP response: %w", err)
			case rejected < 0:
				requestResult.Err = fmt.Errorf("otlp/export: response contains a negative rejected-item count: %d", rejected)
			case rejected > 0:
				requestResult.RejectedItems = rejected
				if diagnostic == "" {
					requestResult.Err = fmt.Errorf("otlp/export: intake rejected %d item(s)", rejected)
				} else {
					requestResult.Err = fmt.Errorf("otlp/export: intake rejected %d item(s): %s", rejected, diagnostic)
				}
			}
		}
		result.Requests = append(result.Requests, requestResult)
	}
	result.finalize()
	return result, aggregateFailures(result)
}

func isNilMessage[T proto.Message](message T) bool {
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
