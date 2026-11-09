// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build lambda_metric

package main

import (
	"context"

	ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
	"github.com/aws/aws-lambda-go/lambda"
)

// The Metric variant imports ddlambda, so it is only built with the
// lambda_metric tag. The other variants run in a build without the import.
func init() {
	taggedVariants["Metric"] = func() {
		lambda.Start(func(ctx context.Context) (string, error) {
			markInvocation(ctx)
			// No context argument: ddlambda finds the invocation through its global.
			ddlambda.Metric("lambda.test.metric", 1, "variant:metric")
			return "ok", nil
		})
	}
}
