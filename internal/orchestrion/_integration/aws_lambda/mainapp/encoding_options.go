// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	"github.com/aws/aws-lambda-go/lambda"
)

func startEncodingOptions() {
	opts := []lambda.Option{lambda.WithSetEscapeHTML(false), lambda.WithSetIndent("", "  ")}
	lambda.StartWithOptions(func(ctx context.Context, _ event) (map[string]string, error) {
		return map[string]string{"value": "<response>"}, work(ctx)
	}, opts...)
}
