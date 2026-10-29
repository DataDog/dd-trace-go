// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
)

func startUseNumber() {
	lambda.StartWithOptions(func(ctx context.Context, event map[string]any) (string, error) {
		number, ok := event["id"].(json.Number)
		if !ok {
			return "", fmt.Errorf("expected json.Number, got %T", event["id"])
		}
		return number.String(), work(ctx)
	}, lambda.WithUseNumber(true))
}
