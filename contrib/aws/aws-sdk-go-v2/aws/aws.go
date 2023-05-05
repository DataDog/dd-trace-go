// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"fmt"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/internal/awsnamingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const componentName = "aws/aws-sdk-go-v2/aws"

func init() {
	telemetry.LoadIntegration(componentName)
}

const (
	tagAWSAgent     = "aws.agent"
	tagAWSService   = "aws.service"
	tagAWSOperation = "aws.operation"
	tagAWSRegion    = "aws.region"
	tagAWSRequestID = "aws.request_id"
)

type spanTimestampKey struct{}

// AppendMiddleware takes the aws.Config and adds the Datadog tracing middleware into the APIOptions middleware stack.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	cfg := &config{}

	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	tm := traceMiddleware{cfg: cfg}
	awsCfg.APIOptions = append(awsCfg.APIOptions, tm.initTraceMiddleware, tm.startTraceMiddleware, tm.deserializeTraceMiddleware)
}

type traceMiddleware struct {
	cfg *config
}

func (mw *traceMiddleware) initTraceMiddleware(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("InitTraceMiddleware", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		// Bind the timestamp to the context so that we can use it when we have enough information to start the trace.
		ctx = context.WithValue(ctx, spanTimestampKey{}, time.Now())
		return next.HandleInitialize(ctx, in)
	}), middleware.Before)
}

func (mw *traceMiddleware) startTraceMiddleware(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("StartTraceMiddleware", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		operation := awsmiddleware.GetOperationName(ctx)
		serviceID := awsmiddleware.GetServiceID(ctx)

		opts := []ddtrace.StartSpanOption{
			tracer.SpanType(ext.SpanTypeHTTP),
			tracer.ServiceName(serviceName(mw.cfg, serviceID)),
			tracer.ResourceName(fmt.Sprintf("%s.%s", serviceID, operation)),
			tracer.Tag(tagAWSRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tagAWSOperation, operation),
			tracer.Tag(tagAWSService, serviceID),
			tracer.StartTime(ctx.Value(spanTimestampKey{}).(time.Time)),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		}
		if !math.IsNaN(mw.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, mw.cfg.analyticsRate))
		}
		span, spanctx := tracer.StartSpanFromContext(ctx, spanName(serviceID, operation), opts...)

		// Handle initialize and continue through the middleware chain.
		out, metadata, err = next.HandleInitialize(spanctx, in)
		span.Finish(tracer.WithError(err))

		return out, metadata, err
	}), middleware.After)
}

func (mw *traceMiddleware) deserializeTraceMiddleware(stack *middleware.Stack) error {
	return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("DeserializeTraceMiddleware", func(
		ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
	) (
		out middleware.DeserializeOutput, metadata middleware.Metadata, err error,
	) {
		span, _ := tracer.SpanFromContext(ctx)

		// Get values out of the request.
		if req, ok := in.Request.(*smithyhttp.Request); ok {
			// Make a copy of the URL so we don't modify the outgoing request
			url := *req.URL
			url.User = nil // Do not include userinfo in the HTTPURL tag.
			span.SetTag(ext.HTTPMethod, req.Method)
			span.SetTag(ext.HTTPURL, url.String())
			span.SetTag(tagAWSAgent, req.Header.Get("User-Agent"))
		}

		// Continue through the middleware chain which eventually sends the request.
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
	}), middleware.Before)
}

func spanName(awsService, awsOperation string) string {
	return awsnamingschema.NewAWSOutboundOp(awsService, awsOperation, func(s string) string {
		return s + ".request"
	}).GetName()
}

func serviceName(cfg *config, awsService string) string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}
	defaultName := fmt.Sprintf("aws.%s", awsService)
	return namingschema.NewServiceNameSchema(
		"",
		defaultName,
		namingschema.WithVersionOverride(namingschema.SchemaV0, defaultName),
	).GetName()
}
