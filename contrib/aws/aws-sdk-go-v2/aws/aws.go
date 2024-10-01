// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"math"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/internal/tags"
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
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const componentName = "aws/aws-sdk-go-v2/aws"

func init() {
	log.Debug("[nhulston tracer] AWS v2 init()")
	fmt.Println("[nhulston tracer] AWS v2 init() println")
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/aws/aws-sdk-go-v2")
}

type spanTimestampKey struct{}

// AppendMiddleware takes the aws.Config and adds the Datadog tracing middleware into the APIOptions middleware stack.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	log.Debug("[nhulston tracer] AppendMiddleware()")
	fmt.Println("[nhulston tracer] AppendMiddleware()")
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
	log.Debug("[nhulston tracer] AWS v2 initTraceMiddleware()")
	fmt.Println("[nhulston tracer] AWS v2 initTraceMiddleware() println")
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
	log.Debug("[nhulston tracer] AWS v2 startTraceMiddleware()")
	fmt.Println("[nhulston tracer] AWS v2 startTraceMiddleware()")
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
			tracer.Tag(tags.OldAWSRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tags.AWSRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tags.AWSOperation, operation),
			tracer.Tag(tags.OldAWSService, serviceID),
			tracer.Tag(tags.AWSService, serviceID),
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
		if err != nil && (mw.cfg.errCheck == nil || mw.cfg.errCheck(err)) {
			span.SetTag(ext.Error, err)
		}
		span.Finish()

		return out, metadata, err
	}), middleware.After)
}

func resourceNameFromParams(requestInput middleware.InitializeInput, awsService string) (string, string, error) {
	log.Debug("[nhulston tracer] AWS v2 resourceNameFromParams()")
	fmt.Println("[nhulston tracer] AWS v2 resourceNameFromParams()")
	var k, v string

	switch awsService {
	case "SQS":
		k, v = tags.SQSQueueName, queueName(requestInput)
	case "S3":
		k, v = tags.S3BucketName, bucketName(requestInput)
	case "SNS":
		k, v = destinationTagValue(requestInput)
	case "DynamoDB":
		k, v = tags.DynamoDBTableName, tableName(requestInput)
	case "Kinesis":
		k, v = tags.KinesisStreamName, streamName(requestInput)
	case "EventBridge":
		k, v = tags.EventBridgeRuleName, ruleName(requestInput)
	case "SFN":
		k, v = tags.SFNStateMachineName, stateMachineName(requestInput)
	default:
		return "", "", fmt.Errorf("attemped to extract ResourceNameFromParams of an unsupported AWS service: %s", awsService)
	}

	return k, v, nil
}

func queueName(requestInput middleware.InitializeInput) string {
	fmt.Println("[nhulston tracer] queueName()")
	log.Debug("[nhulston tracer] queueName()")
	var queueURL string
	switch params := requestInput.Parameters.(type) {
	case *sqs.SendMessageInput:
		queueURL = *params.QueueUrl
		// Inject "foo": "bar" into the message attributes
		fmt.Println("[nhulston tracer] trying to inject foobar")
		log.Debug("[nhulston tracer] trying to inject foobar()")

		if params.MessageAttributes == nil {
			fmt.Println("[nhulston tracer] message attributes was nil")
			log.Debug("[nhulston tracer] message attributes was nil")
			params.MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		fmt.Println("[nhulston tracer] setting foobar")
		log.Debug("[nhulston tracer] setting foobar")
		params.MessageAttributes["foo"] = types.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String("bar"),
		}
		params.MessageBody = aws.String("foobar")
		fmt.Println("[nhulston tracer] done setting foobar")
		log.Debug("[nhulston tracer] done setting foobar")
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
	fmt.Println("[nhulston tracer] bucketName()")
	log.Debug("[nhulston tracer] bucketName()")
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

func destinationTagValue(requestInput middleware.InitializeInput) (tag string, value string) {
	fmt.Println("[nhulston tracer] destinationTagValue()")
	log.Debug("[nhulston tracer] destinationTagValue()")
	tag = tags.SNSTopicName
	var s string
	switch params := requestInput.Parameters.(type) {
	case *sns.PublishInput:
		switch {
		case params.TopicArn != nil:
			s = *params.TopicArn
		case params.TargetArn != nil:
			tag = tags.SNSTargetName
			s = *params.TargetArn
		default:
			return "destination", "empty"
		}
	case *sns.PublishBatchInput:
		s = *params.TopicArn
	case *sns.GetTopicAttributesInput:
		s = *params.TopicArn
	case *sns.ListSubscriptionsByTopicInput:
		s = *params.TopicArn
	case *sns.RemovePermissionInput:
		s = *params.TopicArn
	case *sns.SetTopicAttributesInput:
		s = *params.TopicArn
	case *sns.SubscribeInput:
		s = *params.TopicArn
	case *sns.CreateTopicInput:
		return tag, *params.Name
	}
	parts := strings.Split(s, ":")
	return tag, parts[len(parts)-1]
}

func tableName(requestInput middleware.InitializeInput) string {
	fmt.Println("[nhulston tracer] tableName()")
	log.Debug("[nhulston tracer] tableName()")
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
	fmt.Println("[nhulston tracer] streamName()")
	log.Debug("[nhulston tracer] streamName()")
	switch params := requestInput.Parameters.(type) {
	case *kinesis.PutRecordInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.PutRecordsInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.AddTagsToStreamInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.RemoveTagsFromStreamInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.CreateStreamInput:
		if params.StreamName != nil {
			return *params.StreamName
		}
	case *kinesis.DeleteStreamInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.DescribeStreamInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
	case *kinesis.DescribeStreamSummaryInput:
		return coalesceNameOrArnResource(params.StreamName, params.StreamARN)
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
	fmt.Println("[nhulston tracer] ruleName()")
	log.Debug("[nhulston tracer] ruleName()")
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
	fmt.Println("[nhulston tracer] stateMachineName()")
	log.Debug("[nhulston tracer] stateMachineName()")
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
	fmt.Println("[nhulston tracer] deserializeTraceMiddleware()")
	log.Debug("[nhulston tracer] deserializeTraceMiddleware()")
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
			span.SetTag(tags.AWSAgent, req.Header.Get("User-Agent"))
		}

		// Continue through the middleware chain which eventually sends the request.
		out, metadata, err = next.HandleDeserialize(ctx, in)

		// Get values out of the response.
		if res, ok := out.RawResponse.(*smithyhttp.Response); ok {
			span.SetTag(ext.HTTPCode, res.StatusCode)
		}

		// Extract the request id.
		if requestID, ok := awsmiddleware.GetRequestIDMetadata(metadata); ok {
			span.SetTag(tags.AWSRequestID, requestID)
		}

		return out, metadata, err
	}), middleware.Before)
}

func spanName(awsService, awsOperation string) string {
	fmt.Println("[nhulston tracer] spanName()")
	log.Debug("[nhulston tracer] spanName()")
	return namingschema.AWSOpName(awsService, awsOperation, awsService+".request")
}

func serviceName(cfg *config, awsService string) string {
	fmt.Println("[nhulston tracer] serviceName()")
	log.Debug("[nhulston tracer] serviceName()")
	if cfg.serviceName != "" {
		return cfg.serviceName
	}
	defaultName := fmt.Sprintf("aws.%s", awsService)
	return namingschema.ServiceNameOverrideV0(defaultName, defaultName)
}

func coalesceNameOrArnResource(name *string, arnVal *string) string {
	fmt.Println("[nhulston tracer] coalesceNameOrArnResource()")
	log.Debug("[nhulston tracer] coalesceNameOrArnResource()")
	if name != nil {
		return *name
	}

	if arnVal != nil {
		parts := strings.Split(*arnVal, "/")
		return parts[len(parts)-1]
	}

	return ""
}
