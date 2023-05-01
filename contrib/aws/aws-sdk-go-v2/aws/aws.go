// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const componentName = "aws/aws-sdk-go-v2/aws"

func init() {
	telemetry.LoadIntegration(componentName)
}

const (
	tagAWSAgent           = "aws.agent"
	tagAWSService         = "aws.service"
	tagTopLevelAWSService = "aws_service"
	tagAWSOperation       = "aws.operation"
	tagAWSRegion          = "aws.region"
	tagTopLevelRegion     = "region"
	tagAWSRequestID       = "aws.request_id"
	tagQueueName          = "queuename"
	tagTopicName          = "topicname"
	tagTableName          = "tablename"
	tagStreamName         = "streamname"
	tagBucketName         = "bucketname"
	tagRuleName           = "rulename"
	tagStateMachineName   = "statemachinename"
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
			tracer.Tag(tagTopLevelRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tagAWSOperation, operation),
			tracer.Tag(tagAWSService, serviceID),
			tracer.Tag(tagTopLevelAWSService, serviceID),
			tracer.StartTime(ctx.Value(spanTimestampKey{}).(time.Time)),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		}
		resourceNameKey, resourceNameValue, err := extractResourceNameFromParams(in, serviceID)
		if err != nil {
			log.Printf("Error: %v", err)
		}
		opts = append(opts, tracer.Tag(resourceNameKey, resourceNameValue))
		if !math.IsNaN(mw.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, mw.cfg.analyticsRate))
		}
		span, spanctx := tracer.StartSpanFromContext(ctx, fmt.Sprintf("%s.request", serviceID), opts...)

		// Handle initialize and continue through the middleware chain.
		out, metadata, err = next.HandleInitialize(spanctx, in)
		span.Finish(tracer.WithError(err))

		return out, metadata, err
	}), middleware.After)
}

func extractResourceNameFromParams(requestInput middleware.InitializeInput, awsService string) (string, string, error) {
	var resourceNameKey, resourceNameValue string

	switch awsService {
	case "SQS":
		resourceNameKey = tagQueueName
		resourceNameValue = extractQueueName(requestInput)
	case "S3":
		resourceNameKey = tagBucketName
		resourceNameValue = extractBucketName(requestInput)
	case "SNS":
		resourceNameKey = tagTopicName
		resourceNameValue = extractTopicName(requestInput)
	case "DynamoDB":
		resourceNameKey = tagTableName
		resourceNameValue = extractTableName(requestInput)
	case "Kinesis":
		resourceNameKey = tagStreamName
		resourceNameValue = extractStreamName(requestInput)
	case "EventBridge":
		resourceNameKey = tagRuleName
		resourceNameValue = extractRuleName(requestInput)
	case "SFN":
		resourceNameKey = tagStateMachineName
		resourceNameValue = extractStateMachineName(requestInput)
	default:
		return "", "", fmt.Errorf("attemped to extract ResourceNameFromParams of an unsupported AWS service: %s", awsService)
	}

	return resourceNameKey, resourceNameValue, nil
}

func extractQueueName(requestInput middleware.InitializeInput) string {
	var queueURL *string
	switch params := requestInput.Parameters.(type) {
	case *sqs.SendMessageInput:
		queueURL = params.QueueUrl
	case *sqs.DeleteMessageInput:
		queueURL = params.QueueUrl
	case *sqs.DeleteMessageBatchInput:
		queueURL = params.QueueUrl
	case *sqs.ReceiveMessageInput:
		queueURL = params.QueueUrl
	case *sqs.SendMessageBatchInput:
		queueURL = params.QueueUrl
	}
	if queueURL == nil {
		return ""
	}
	parts := strings.Split(*queueURL, "/")
	return parts[len(parts)-1]
}

func extractBucketName(requestInput middleware.InitializeInput) string {
	var bucket *string
	switch params := requestInput.Parameters.(type) {
	case *s3.ListObjectsInput:
		bucket = params.Bucket
	case *s3.ListObjectsV2Input:
		bucket = params.Bucket
	case *s3.PutObjectInput:
		bucket = params.Bucket
	case *s3.GetObjectInput:
		bucket = params.Bucket
	case *s3.DeleteObjectInput:
		bucket = params.Bucket
	case *s3.DeleteObjectsInput:
		bucket = params.Bucket
	}
	if bucket == nil {
		return ""
	}
	return *bucket
}

func extractTopicName(requestInput middleware.InitializeInput) string {
	var topicArn *string
	switch params := requestInput.Parameters.(type) {
	case *sns.PublishInput:
		topicArn = params.TopicArn
	case *sns.PublishBatchInput:
		topicArn = params.TopicArn
	case *sns.GetTopicAttributesInput:
		topicArn = params.TopicArn
	case *sns.ListSubscriptionsByTopicInput:
		topicArn = params.TopicArn
	case *sns.RemovePermissionInput:
		topicArn = params.TopicArn
	case *sns.SetTopicAttributesInput:
		topicArn = params.TopicArn
	case *sns.SubscribeInput:
		topicArn = params.TopicArn
	case *sns.CreateTopicInput:
		return *params.Name
	}
	if topicArn == nil {
		return ""
	}
	parts := strings.Split(*topicArn, ":")
	return parts[len(parts)-1]
}

func extractTableName(requestInput middleware.InitializeInput) string {
	var tableName *string
	switch params := requestInput.Parameters.(type) {
	case *dynamodb.GetItemInput:
		tableName = params.TableName
	case *dynamodb.PutItemInput:
		tableName = params.TableName
	case *dynamodb.QueryInput:
		tableName = params.TableName
	case *dynamodb.ScanInput:
		tableName = params.TableName
	case *dynamodb.UpdateItemInput:
		tableName = params.TableName
	}
	if tableName == nil {
		return ""
	}
	return *tableName
}

func extractStreamName(requestInput middleware.InitializeInput) string {
	var streamName *string

	switch params := requestInput.Parameters.(type) {
	case *kinesis.PutRecordInput:
		streamName = params.StreamName
	case *kinesis.PutRecordsInput:
		streamName = params.StreamName
	case *kinesis.AddTagsToStreamInput:
		streamName = params.StreamName
	case *kinesis.RemoveTagsFromStreamInput:
		streamName = params.StreamName
	case *kinesis.CreateStreamInput:
		streamName = params.StreamName
	case *kinesis.DeleteStreamInput:
		streamName = params.StreamName
	case *kinesis.DescribeStreamInput:
		streamName = params.StreamName
	case *kinesis.DescribeStreamSummaryInput:
		streamName = params.StreamName
	case *kinesis.GetRecordsInput:
		if params.StreamARN != nil {
			streamArnValue := *params.StreamARN
			parts := strings.Split(streamArnValue, "/")
			return parts[len(parts)-1]
		}
	}

	if streamName == nil {
		return ""
	}
	return *streamName
}

func extractRuleName(requestInput middleware.InitializeInput) string {
	var ruleName *string

	switch params := requestInput.Parameters.(type) {
	case *eventbridge.PutRuleInput:
		ruleName = params.Name
	case *eventbridge.DescribeRuleInput:
		ruleName = params.Name
	case *eventbridge.DeleteRuleInput:
		ruleName = params.Name
	case *eventbridge.DisableRuleInput:
		ruleName = params.Name
	case *eventbridge.EnableRuleInput:
		ruleName = params.Name
	case *eventbridge.PutTargetsInput:
		ruleName = params.Rule
	case *eventbridge.RemoveTargetsInput:
		ruleName = params.Rule
	}

	if ruleName == nil {
		return ""
	}
	return *ruleName
}

func extractStateMachineName(requestInput middleware.InitializeInput) string {
	var stateMachineArn *string

	switch params := requestInput.Parameters.(type) {
	case *sfn.CreateStateMachineInput:
		return *params.Name
	case *sfn.DescribeStateMachineInput:
		stateMachineArn = params.StateMachineArn
	case *sfn.StartExecutionInput:
		stateMachineArn = params.StateMachineArn
	case *sfn.StopExecutionInput:
		if params.ExecutionArn != nil {
			executionArnValue := *params.ExecutionArn
			parts := strings.Split(executionArnValue, ":")
			return parts[len(parts)-2]
		}
	case *sfn.DescribeExecutionInput:
		if params.ExecutionArn != nil {
			executionArnValue := *params.ExecutionArn
			parts := strings.Split(executionArnValue, ":")
			return parts[len(parts)-2]
		}
	case *sfn.ListExecutionsInput:
		stateMachineArn = params.StateMachineArn
	case *sfn.UpdateStateMachineInput:
		stateMachineArn = params.StateMachineArn
	case *sfn.DeleteStateMachineInput:
		stateMachineArn = params.StateMachineArn
	}

	if stateMachineArn == nil {
		return ""
	}

	parts := strings.Split(*stateMachineArn, ":")
	return parts[len(parts)-1]
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

func serviceName(cfg *config, serviceID string) string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}

	return fmt.Sprintf("aws.%s", serviceID)
}
