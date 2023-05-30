// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/internal/awsnamingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
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
	// duplicate tags that will be phased out in favor of aws_service & region
	tagAWSService = "aws.service"
	tagAWSRegion  = "aws.region"
)
const (
	tagAWSAgent         = "aws.agent"
	tagService          = "aws_service" //Service aws-sdk request is heading to, ex. S3, SQS
	tagAWSOperation     = "aws.operation"
	tagRegion           = "region" //AWS Region used to pivot from AWS Integration metrics to traces
	tagAWSRequestID     = "aws.request_id"
	tagQueueName        = "queuename"
	tagTopicName        = "topicname"
	tagTableName        = "tablename"
	tagStreamName       = "streamname"
	tagBucketName       = "bucketname"
	tagRuleName         = "rulename"
	tagStateMachineName = "statemachinename"
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
			tracer.Tag(tagRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tagAWSOperation, operation),
			tracer.Tag(tagAWSService, serviceID),
			tracer.Tag(tagService, serviceID),
			tracer.StartTime(ctx.Value(spanTimestampKey{}).(time.Time)),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		}
		k, v, err := resourceNameFromParams(in, serviceID)
		if err != nil {
			log.Debug("Error: %v", err)
		} else {
			opts = append(opts, tracer.Tag(k, v))
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

func resourceNameFromParams(requestInput middleware.InitializeInput, awsService string) (string, string, error) {
	var k, v string

	switch awsService {
	case "SQS":
		k, v = tagQueueName, queueName(requestInput)
	case "S3":
		k, v = tagBucketName, bucketName(requestInput)
	case "SNS":
		k, v = tagTopicName, topicName(requestInput)
	case "DynamoDB":
		k, v = tagTableName, tableName(requestInput)
	case "Kinesis":
		k, v = tagStreamName, streamName(requestInput)
	case "EventBridge":
		k, v = tagRuleName, ruleName(requestInput)
	case "SFN":
		k, v = tagStateMachineName, stateMachineName(requestInput)
	default:
		return "", "", fmt.Errorf("attemped to extract ResourceNameFromParams of an unsupported AWS service: %s", awsService)
	}

	return k, v, nil
}

func queueName(requestInput middleware.InitializeInput) string {
	var queueURL string
	switch params := requestInput.Parameters.(type) {
	case *sqs.SendMessageInput:
		queueURL = *params.QueueUrl
	case *sqs.DeleteMessageInput:
		queueURL = *params.QueueUrl
	case *sqs.DeleteMessageBatchInput:
		queueURL = *params.QueueUrl
	case *sqs.ReceiveMessageInput:
		queueURL = *params.QueueUrl
	case *sqs.SendMessageBatchInput:
		queueURL = *params.QueueUrl
	}
	parts := strings.Split(queueURL, "/")
	return parts[len(parts)-1]
}

func bucketName(requestInput middleware.InitializeInput) string {
	switch params := requestInput.Parameters.(type) {
	case *s3.ListObjectsInput:
		return *params.Bucket
	case *s3.ListObjectsV2Input:
		return *params.Bucket
	case *s3.PutObjectInput:
		return *params.Bucket
	case *s3.GetObjectInput:
		return *params.Bucket
	case *s3.DeleteObjectInput:
		return *params.Bucket
	case *s3.DeleteObjectsInput:
		return *params.Bucket
	}
	return ""
}

func topicName(requestInput middleware.InitializeInput) string {
	var topicArn string
	switch params := requestInput.Parameters.(type) {
	case *sns.PublishInput:
		topicArn = *params.TopicArn
	case *sns.PublishBatchInput:
		topicArn = *params.TopicArn
	case *sns.GetTopicAttributesInput:
		topicArn = *params.TopicArn
	case *sns.ListSubscriptionsByTopicInput:
		topicArn = *params.TopicArn
	case *sns.RemovePermissionInput:
		topicArn = *params.TopicArn
	case *sns.SetTopicAttributesInput:
		topicArn = *params.TopicArn
	case *sns.SubscribeInput:
		topicArn = *params.TopicArn
	case *sns.CreateTopicInput:
		return *params.Name
	}
	parts := strings.Split(topicArn, ":")
	return parts[len(parts)-1]
}

func tableName(requestInput middleware.InitializeInput) string {
	switch params := requestInput.Parameters.(type) {
	case *dynamodb.GetItemInput:
		return *params.TableName
	case *dynamodb.PutItemInput:
		return *params.TableName
	case *dynamodb.QueryInput:
		return *params.TableName
	case *dynamodb.ScanInput:
		return *params.TableName
	case *dynamodb.UpdateItemInput:
		return *params.TableName
	}
	return ""
}

func streamName(requestInput middleware.InitializeInput) string {
	switch params := requestInput.Parameters.(type) {
	case *kinesis.PutRecordInput:
		return *params.StreamName
	case *kinesis.PutRecordsInput:
		return *params.StreamName
	case *kinesis.AddTagsToStreamInput:
		return *params.StreamName
	case *kinesis.RemoveTagsFromStreamInput:
		return *params.StreamName
	case *kinesis.CreateStreamInput:
		return *params.StreamName
	case *kinesis.DeleteStreamInput:
		return *params.StreamName
	case *kinesis.DescribeStreamInput:
		return *params.StreamName
	case *kinesis.DescribeStreamSummaryInput:
		return *params.StreamName
	case *kinesis.GetRecordsInput:
		if params.StreamARN != nil {
			streamArnValue := *params.StreamARN
			parts := strings.Split(streamArnValue, "/")
			return parts[len(parts)-1]
		}
	}
	return ""
}

func ruleName(requestInput middleware.InitializeInput) string {
	switch params := requestInput.Parameters.(type) {
	case *eventbridge.PutRuleInput:
		return *params.Name
	case *eventbridge.DescribeRuleInput:
		return *params.Name
	case *eventbridge.DeleteRuleInput:
		return *params.Name
	case *eventbridge.DisableRuleInput:
		return *params.Name
	case *eventbridge.EnableRuleInput:
		return *params.Name
	case *eventbridge.PutTargetsInput:
		return *params.Rule
	case *eventbridge.RemoveTargetsInput:
		return *params.Rule
	}
	return ""
}

func stateMachineName(requestInput middleware.InitializeInput) string {
	var stateMachineArn string

	switch params := requestInput.Parameters.(type) {
	case *sfn.CreateStateMachineInput:
		return *params.Name
	case *sfn.DescribeStateMachineInput:
		stateMachineArn = *params.StateMachineArn
	case *sfn.StartExecutionInput:
		stateMachineArn = *params.StateMachineArn
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
		stateMachineArn = *params.StateMachineArn
	case *sfn.UpdateStateMachineInput:
		stateMachineArn = *params.StateMachineArn
	case *sfn.DeleteStateMachineInput:
		stateMachineArn = *params.StateMachineArn
	}
	parts := strings.Split(stateMachineArn, ":")
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
