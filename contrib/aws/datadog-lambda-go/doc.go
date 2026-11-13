// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ddlambda instruments AWS Lambda handlers written with aws-lambda-go.
// Wrap the handler by hand:
//
//	lambda.Start(ddlambda.WrapFunction(handler, nil))
//
// WrapFunction accepts handler functions. WrapLambdaHandlerInterface accepts
// lambda.Handler values, such as the result of lambda.NewHandler. A nil Config
// reads configuration from DD_* environment variables.
//
// # Orchestrion
//
// The Orchestrion integration in this package is part of
// github.com/DataDog/dd-trace-go/orchestrion/all. A project set up for
// Orchestrion wraps the handler passed to lambda.Start, lambda.StartWithOptions,
// lambda.StartWithContext, lambda.StartHandler, lambda.StartHandlerWithContext,
// and lambda.StartHandlerFunc. The hook runs inside aws-lambda-go after handler
// options such as lambda.WithUseNumber and lambda.WithContext have been applied.
// Configuration is read from DD_* environment variables.
//
// Continuing traces from upstream services into the function, for event
// sources other than API Gateway, requires the Datadog Lambda Extension.
package ddlambda
