// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
)

func startPrebuiltContext() {
	type key string
	ctx := context.WithValue(context.Background(), key("base"), "base-context")
	handler := lambda.NewHandlerWithOptions(func(ctx context.Context, _ event) (string, error) {
		value := fmt.Sprintf("%v/%v", ctx.Value(key("base")), ctx.Value(key("option")))
		return value, work(ctx)
	}, lambda.WithContext(ctx), lambda.WithContextValue(key("option"), "context-value"))
	lambda.Start(handler)
}
