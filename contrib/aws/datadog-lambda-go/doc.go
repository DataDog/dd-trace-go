// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package ddlambda instruments AWS Lambda functions written with
// github.com/aws/aws-lambda-go. It reports enhanced Lambda metrics, traces
// each invocation, and submits custom metrics.
//
// Wrap the handler passed to lambda.Start:
//
//	lambda.Start(ddlambda.WrapFunction(handler, nil))
//
// WrapFunction accepts handler functions and lambda.Handler values, such as
// the result of lambda.NewHandler. A handler that ddlambda already wrapped is
// returned unchanged, so it is never instrumented twice.
//
// # Orchestrion
//
// The Orchestrion integration instruments the common runtime entry point used by
// every lambda.Start* API, with configuration read from DD_* environment variables.
// AWS keeps its original configured handler, so its request decoding, response
// encoding, and context options also apply under Orchestrion. This includes options
// on prebuilt lambda.NewHandlerWithOptions handlers. A handler already wrapped by
// hand is not instrumented again, including through lambda.NewHandler.
// The integration is part of github.com/DataDog/dd-trace-go/orchestrion/all.
//
// Continuing traces from upstream services into the function, for event
// sources other than API Gateway, requires the Datadog Lambda Extension.
package ddlambda
