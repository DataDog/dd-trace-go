// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wrapper

import (
	"context"
	"encoding/json"
	"io"

	"github.com/aws/aws-lambda-go/lambda/handlertrace"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
)

// WrapRuntimeHandlerWithListeners instruments AWS's configured invocation adapter
// without changing its event type, decoding options, or response reader.
func WrapRuntimeHandlerWithListeners(handler func(context.Context, []byte) (io.Reader, error), listeners ...HandlerListener) func(context.Context, []byte) (io.Reader, error) {
	coldStart := true
	return func(ctx context.Context, payload []byte) (io.Reader, error) {
		//nolint:staticcheck // Existing listeners use this context key.
		ctx = context.WithValue(ctx, "cold_start", coldStart)
		msg := json.RawMessage(payload)
		for _, listener := range listeners {
			ctx, msg = listener.HandlerStarted(ctx, msg)
		}
		CurrentContext = ctx
		defer func() {
			coldStart = false
			CurrentContext = nil
		}()

		// AWS exposes the original response through this hook. Preserve existing
		// hooks and leave serialization (including streaming readers) to AWS.
		var response any
		ctx = handlertrace.NewContext(ctx, handlertrace.HandlerTrace{
			ResponseEvent: func(_ context.Context, event any) { response = event },
		})
		result, err := handler(ctx, msg)
		if response != nil {
			ctx = context.WithValue(ctx, extension.DdLambdaResponse, response)
		}
		for _, listener := range listeners {
			listener.HandlerFinished(ctx, err)
		}
		return result, err
	}
}
