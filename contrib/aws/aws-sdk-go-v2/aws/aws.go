// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"fmt"
	"math"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	tagAWSAgent     = "aws.agent"
	tagAWSService   = "aws.service"
	tagAWSOperation = "aws.operation"
	tagAWSRegion    = "aws.region"
	tagAWSRequestID = "aws.request_id"
)

// AppendMiddleware takes the API options from the aws.Config.
// Middleware allows us to add middleware to the AWS SDK GO v2.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	cfg := &config{}

	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	tm := traceMiddleware{cfg: cfg}
	awsCfg.APIOptions = append(awsCfg.APIOptions, tm.startTrace, tm.initTrace, tm.deserializeTrace)
}

type traceMiddleware struct {
	cfg *config
}

func (mw *traceMiddleware) startTrace(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("TraceStart", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		// Start a span with the minimum information possible.
		// If we get a failure in the Init middleware, some context is better than none.
		opts := []ddtrace.StartSpanOption{
			tracer.SpanType(ext.SpanTypeHTTP),
			tracer.ServiceName(serviceName(mw.cfg, "unknown")),
		}
		if !math.IsNaN(mw.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, mw.cfg.analyticsRate))
		}
		span, spanctx := tracer.StartSpanFromContext(ctx, "unknown.request", opts...)

		// Handle Initialize and continue through the middleware chain.
		out, metadata, err = next.HandleInitialize(spanctx, in)
		if err != nil {
			span.Finish(tracer.WithError(err))
		} else {
			span.Finish()
		}

		return out, metadata, err
	}), middleware.Before)
}

func (mw *traceMiddleware) initTrace(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("TraceInit", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		span, ok := tracer.SpanFromContext(ctx)
		if !ok {
			// If no span is found then we don't need to enrich the trace so just continue.
			return next.HandleInitialize(ctx, in)
		}

		// As we run this middleware After other Initialize middlewares, we have access to more metadata.
		operation := awsmiddleware.GetOperationName(ctx)
		serviceID := awsmiddleware.GetServiceID(ctx)
		span.SetTag(ext.ServiceName, serviceName(mw.cfg, serviceID))
		span.SetTag(ext.SpanName, fmt.Sprintf("%s.request", serviceID))
		span.SetTag(ext.ResourceName, fmt.Sprintf("%s.%s", serviceID, operation))
		span.SetTag(tagAWSRegion, awsmiddleware.GetRegion(ctx))
		span.SetTag(tagAWSOperation, awsmiddleware.GetOperationName(ctx))
		span.SetTag(tagAWSService, serviceID)

		return next.HandleInitialize(ctx, in)
	}), middleware.After)
}

func (mw *traceMiddleware) deserializeTrace(stack *middleware.Stack) error {
	return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("TraceDeserialize", func(
		ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
	) (
		out middleware.DeserializeOutput, metadata middleware.Metadata, err error,
	) {
		span, ok := tracer.SpanFromContext(ctx)
		if !ok {
			// If no span is found then we don't need to enrich the trace so just continue.
			return next.HandleDeserialize(ctx, in)
		}

		// Get values out of the request.
		if req, ok := in.Request.(*smithyhttp.Request); ok {
			span.SetTag(ext.HTTPMethod, req.Method)
			span.SetTag(ext.HTTPURL, req.URL.String())
			span.SetTag(tagAWSAgent, req.Header.Get("User-Agent"))
		}

		// Continue through the middleware layers, eventually sending the request.
		out, metadata, err = next.HandleDeserialize(ctx, in)

		// Get values out of the response.
		if res, ok := out.RawResponse.(*smithyhttp.Response); ok {
			span.SetTag(ext.HTTPCode, res.StatusCode)
		}

		// Extract the request id.
		if requestID, ok := awsmiddleware.GetRequestIDMetadata(metadata); ok {
			span.SetTag(tagAWSRequestID, requestID)
		}

		return out, metadata, err
	}), middleware.After)
}

func serviceName(cfg *config, serviceID string) string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}

	if serviceID == "" {
		serviceID = "unknown"
	}

	return fmt.Sprintf("aws.%s", serviceID)
}
