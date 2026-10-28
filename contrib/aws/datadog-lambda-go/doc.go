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
// The Orchestrion integration in this package rewrites every call to
// lambda.Start, lambda.StartWithOptions, lambda.StartWithContext,
// lambda.StartHandler and lambda.StartHandlerWithContext so that the handler
// goes through WrapFunction (or WrapLambdaHandlerInterface) with a nil Config.
// That is equivalent to wrapping it by hand, with configuration read from DD_*
// environment variables. Options passed to lambda.StartWithOptions are kept,
// and a handler that is already wrapped by hand is not wrapped again.
//
// The integration is opt-in: the github.com/DataDog/dd-trace-go/orchestrion/all
// package does not include it. To enable it, import this package in your
// orchestrion.tool.go file:
//
//	import _ "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2" // integration
//
// Continuing traces from upstream services into the function, for event
// sources other than API Gateway, requires the Datadog Lambda Extension.
package ddlambda
