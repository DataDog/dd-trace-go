// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/aws/aws-lambda-go/lambda"

func startDisallowUnknownFields() {
	lambda.StartWithOptions(handler, lambda.WithDisallowUnknownFields(true))
}
