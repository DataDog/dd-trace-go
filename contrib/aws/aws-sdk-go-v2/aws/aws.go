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
	"sync"
	"time"

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

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	eventBridgeTracer "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal/eventbridge"
	sfnTracer "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal/sfn"
	snsTracer "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal/sns"
	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal/spanpointers"
	sqsTracer "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal/sqs"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "aws/aws-sdk-go-v2/aws"

var instr = internal.Instr

var tagMapPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]string, 2)
	},
}

type spanTimestampKey struct{}

// AppendMiddleware takes the aws.Config and adds the Datadog tracing middleware into the APIOptions middleware stack.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	cfg := &config{}

	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
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
		region := awsmiddleware.GetRegion(ctx)
		partition := awsmiddleware.GetPartitionID(ctx)

		// if partition ID isn't set, derive partition from region
		if partition == "" {
			partition = awsPartition(region)
		}

		opts := []tracer.StartSpanOption{
			tracer.SpanType(ext.SpanTypeHTTP),
			tracer.ServiceName(serviceName(mw.cfg, serviceID)),
			tracer.ResourceName(fmt.Sprintf("%s.%s", serviceID, operation)),
			tracer.Tag(ext.AWSRegionLegacy, region),
			tracer.Tag(ext.AWSRegion, region),
			tracer.Tag(ext.AWSPartition, partition),
			tracer.Tag(ext.AWSOperation, operation),
			tracer.Tag(ext.AWSServiceLegacy, serviceID),
			tracer.Tag(ext.AWSService, serviceID),
			tracer.StartTime(ctx.Value(spanTimestampKey{}).(time.Time)),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		}
		resourceTags, ok := resourceTagsFromParams(in, serviceID, region, partition)
		if !ok {
			instr.Logger().Debug("attempted to extract resourceTagsFromParams of an unsupported AWS service: %s", serviceID)
		} else {
			for k, v := range resourceTags {
				if v != "" {
					opts = append(opts, tracer.Tag(k, v))
				}
				delete(resourceTags, k)
			}
			tagMapPool.Put(resourceTags)
		}

		if !math.IsNaN(mw.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, mw.cfg.analyticsRate))
		}
		span, spanctx := tracer.StartSpanFromContext(ctx, spanName(serviceID, operation), opts...)

		// Inject trace context
		switch serviceID {
		case "SQS":
			sqsTracer.EnrichOperation(span, in, operation)
		case "SNS":
			snsTracer.EnrichOperation(span, in, operation)
		case "EventBridge":
			eventBridgeTracer.EnrichOperation(span, in, operation)
		case "SFN":
			sfnTracer.EnrichOperation(span, in, operation)
		case "DynamoDB":
			spanctx = spanpointers.SetDynamoDbParamsOnContext(spanctx, in.Parameters)
		}

		// Handle initialize and continue through the middleware chain.
		out, metadata, err = next.HandleInitialize(spanctx, in)
		if err != nil && (mw.cfg.errCheck == nil || mw.cfg.errCheck(err)) {
			span.SetTag(ext.Error, err)
		}
		span.Finish()

		return out, metadata, err
	}), middleware.After)
}

func awsPartition(region string) string {
	var partition string
	switch {
	case strings.HasPrefix(region, "cn-"):
		partition = "aws-cn"
	case strings.HasPrefix(region, "us-gov-"):
		partition = "aws-us-gov"
	default:
		partition = "aws"
	}

	return partition
}

func resourceTagsFromParams(requestInput middleware.InitializeInput, awsService string, region string, partition string) (map[string]string, bool) {
	tags := tagMapPool.Get().(map[string]string)

	switch awsService {
	case "SQS":
		if url := queueURL(requestInput); url != "" {
			queueName, arn := extractSQSMetadata(url, region, partition)
			tags[ext.SQSQueueName] = queueName
			tags[ext.CloudResourceID] = arn
		}
	case "S3":
		tags[ext.S3BucketName] = bucketName(requestInput)
	case "SNS":
		k, v := destinationTagValue(requestInput)
		tags[k] = v
	case "DynamoDB":
		tags[ext.DynamoDBTableName] = tableName(requestInput)
	case "Kinesis":
		tags[ext.KinesisStreamName] = streamName(requestInput)
	case "EventBridge":
		tags[ext.EventBridgeRuleName] = ruleName(requestInput)
	case "SFN":
		tags[ext.SFNStateMachineName] = stateMachineName(requestInput)
	default:
		tagMapPool.Put(tags)
		return nil, false
	}

	return tags, true
}

func queueURL(requestInput middleware.InitializeInput) string {
	switch params := requestInput.Parameters.(type) {
	case *sqs.SendMessageInput:
		return *params.QueueUrl
	case *sqs.DeleteMessageInput:
		return *params.QueueUrl
	case *sqs.DeleteMessageBatchInput:
		return *params.QueueUrl
	case *sqs.ReceiveMessageInput:
		return *params.QueueUrl
	case *sqs.SendMessageBatchInput:
		return *params.QueueUrl
	}
	return ""
}

func extractSQSMetadata(queueURL string, region string, partition string) (queueName string, arn string) {
	// Remove trailing slash if present
	if len(queueURL) > 0 && queueURL[len(queueURL)-1] == '/' {
		queueURL = queueURL[:len(queueURL)-1]
	}

	// *.amazonaws.com/{accountID}/{queueName}
	parts := strings.Split(queueURL, "/")
	if len(parts) < 2 {
		return "", ""
	}

	queueName = parts[len(parts)-1]
	accountID := parts[len(parts)-2]

	arn = strings.Join([]string{"arn", partition, "sqs", region, accountID, queueName}, ":")
	return queueName, arn
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

func destinationTagValue(requestInput middleware.InitializeInput) (tag string, value string) {
	tag = ext.SNSTopicName
	var s string
	switch params := requestInput.Parameters.(type) {
	case *sns.PublishInput:
		switch {
		case params.TopicArn != nil:
			s = *params.TopicArn
		case params.TargetArn != nil:
			tag = ext.SNSTargetName
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
	case *dynamodb.DeleteItemInput:
		return *params.TableName
	}
	return ""
}

func streamName(requestInput middleware.InitializeInput) string {
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
			span.SetTag(ext.AWSAgent, req.Header.Get("User-Agent"))
		}

		// Continue through the middleware chain which eventually sends the request.
		out, metadata, err = next.HandleDeserialize(ctx, in)

		// Get values out of the response.
		if res, ok := out.RawResponse.(*smithyhttp.Response); ok {
			span.SetTag(ext.HTTPCode, res.StatusCode)
		}

		// Extract the request id.
		if requestID, ok := awsmiddleware.GetRequestIDMetadata(metadata); ok {
			span.SetTag(ext.AWSRequestID, requestID)
		}

		// Create span pointers
		spanpointers.AddSpanPointers(ctx, in, out, span)

		return out, metadata, err
	}), middleware.Before)
}

func spanName(awsService, awsOperation string) string {
	return instr.OperationName(instrumentation.ComponentDefault, instrumentation.OperationContext{
		ext.AWSService:   awsService,
		ext.AWSOperation: awsOperation,
	})
}

func serviceName(cfg *config, awsService string) string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}
	return instr.ServiceName(instrumentation.ComponentDefault, instrumentation.OperationContext{
		ext.AWSService: awsService,
	})
}

func coalesceNameOrArnResource(name *string, arnVal *string) string {
	if name != nil {
		return *name
	}

	if arnVal != nil {
		parts := strings.Split(*arnVal, "/")
		return parts[len(parts)-1]
	}

	return ""
}
