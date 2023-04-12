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
	tagQueueName 		  = "queuename"
	tagTopicName		  = "topicname"
	tagTableName		  = "tablename"
	tagStreamName		  = "streamname"
	tagBucketName		  = "bucketname"
	tagRuleName		  	  = "rulename"
	tagStateMachineName	  = "statemachinename"
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
		resourceNameKey, resourceNameValue := extractResourceNameFromParams(in, serviceID)
		//queueURL := *in.Parameters.(*sqs.SendMessageInput).QueueUrl
		fmt.Println("resourceNameKey: ", resourceNameKey)
		fmt.Println("resourceNameValue: ", resourceNameValue)
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

func extractResourceNameFromParams(requestInput middleware.InitializeInput, awsService string) (resourceNameKey string, resourceNameValue string) {
	//resourceNameKey := ""
	fmt.Println("awsService: ", awsService)

	switch awsService {
	case "SQS":
		// Extract queueName
		resourceNameKey, resourceNameValue = extractQueueName(requestInput)
	case "S3":
		fmt.Println("got in s3 case")
 
		// Extract bucketName
		resourceNameKey, resourceNameValue = extractBucketName(requestInput)
	case "SNS":
		resourceNameKey, resourceNameValue = extractTopicName(requestInput)
	case "DynamoDB":
		resourceNameKey, resourceNameValue = extractTableName(requestInput)
	case "Kinesis":
		resourceNameKey, resourceNameValue = extractStreamName(requestInput)
	case "EventBridge":
		resourceNameKey, resourceNameValue = extractRuleName(requestInput)
	case "SFN":
		resourceNameKey, resourceNameValue = extractStateMachineName(requestInput)
	} 
    return resourceNameKey, resourceNameValue
}

func extractQueueName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractQueueName")
	queueNameValue := ""
	switch params := requestInput.Parameters.(type) {
	case *sqs.SendMessageInput:
		if params.QueueUrl != nil {
			parts := strings.Split(*params.QueueUrl, "/")
			queueNameValue = parts[len(parts)-1]
		}
	case *sqs.DeleteMessageInput:
		if params.QueueUrl != nil {
			parts := strings.Split(*params.QueueUrl, "/")
			queueNameValue = parts[len(parts)-1]
		}
	case *sqs.DeleteMessageBatchInput:
		if params.QueueUrl != nil {
			parts := strings.Split(*params.QueueUrl, "/")
			queueNameValue = parts[len(parts)-1]
		}
	case *sqs.ReceiveMessageInput:
		if params.QueueUrl != nil {
			parts := strings.Split(*params.QueueUrl, "/")
			queueNameValue = parts[len(parts)-1]
		}
	case *sqs.SendMessageBatchInput:
		if params.QueueUrl != nil {
			parts := strings.Split(*params.QueueUrl, "/")
			queueNameValue = parts[len(parts)-1]
		}
	}
	return tagQueueName, queueNameValue
}

func extractBucketName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractBucketName")
	bucketNameValue := ""
	switch params := requestInput.Parameters.(type) {
	case *s3.ListObjectsInput:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	case *s3.ListObjectsV2Input:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	case *s3.PutObjectInput:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	case *s3.GetObjectInput:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	case *s3.DeleteObjectInput:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	case *s3.DeleteObjectsInput:
		if params.Bucket != nil {
			bucketNameValue = *params.Bucket
		}
	
	}

	return tagBucketName, bucketNameValue
}

func extractTopicName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractTopicName")
	topicNameValue := ""
	switch params := requestInput.Parameters.(type) {
		case *sns.PublishInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.PublishBatchInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.GetTopicAttributesInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.ListSubscriptionsByTopicInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.RemovePermissionInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.SetTopicAttributesInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.SubscribeInput:
			if params.TopicArn != nil {
				topicArnValue := *params.TopicArn
				//example topic_arn: arn:aws:sns:us-west-2:123456789012:my-topic-name
				parts := strings.Split(topicArnValue, ":")
				topicNameValue = parts[len(parts)-1]
			}
		case *sns.CreateTopicInput:
			topicNameValue = *params.Name
	}
	
	return tagTopicName, topicNameValue
}

func extractTableName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractTableName")
	tableNameValue := ""
	switch params := requestInput.Parameters.(type) {
		case *dynamodb.GetItemInput:
			if params.TableName != nil {
				tableNameValue = *params.TableName
			}
		case *dynamodb.PutItemInput:
			if params.TableName != nil {
				tableNameValue = *params.TableName
			}
		case *dynamodb.QueryInput:
			if params.TableName != nil {
				tableNameValue = *params.TableName
			}
		case *dynamodb.ScanInput:
			if params.TableName != nil {
				tableNameValue = *params.TableName
			}
		case *dynamodb.UpdateItemInput:
			if params.TableName != nil {
				tableNameValue = *params.TableName
			}
	}
	
	return tagTableName, tableNameValue
}

func extractStreamName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractStreamName")
	streamNameValue := ""

	switch params := requestInput.Parameters.(type) {
		case *kinesis.PutRecordInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.PutRecordsInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.AddTagsToStreamInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.RemoveTagsFromStreamInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.CreateStreamInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.DeleteStreamInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.DescribeStreamInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.DescribeStreamSummaryInput:
			if params.StreamName != nil {
				streamNameValue = *params.StreamName
			}
		case *kinesis.GetRecordsInput:
			if params.StreamARN != nil {
				streamArnValue := *params.StreamARN //TODO WRITE TESTS IN CASE OF A PANIC
				//example stream_arn: arn:aws:kinesis:us-east-1:123456789012:stream/my-stream
				parts := strings.Split(streamArnValue, "/")
				streamNameValue = parts[len(parts)-1]
			}
	}
	
	return tagStreamName, streamNameValue
}

func extractRuleName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractRuleName")
	ruleNameValue := ""
	switch params := requestInput.Parameters.(type) {
		case *eventbridge.PutRuleInput:
			if params.Name != nil {
				ruleNameValue = *params.Name
			}
		case *eventbridge.DescribeRuleInput:
			if params.Name != nil {
				ruleNameValue = *params.Name
			}
		case *eventbridge.DeleteRuleInput:
			if params.Name != nil {
				ruleNameValue = *params.Name
			}
		case *eventbridge.DisableRuleInput:
			if params.Name != nil {
				ruleNameValue = *params.Name
			}
		case *eventbridge.EnableRuleInput:
			if params.Name != nil {
				ruleNameValue = *params.Name
			}
		case *eventbridge.PutTargetsInput:
			if params.Rule != nil {
				ruleNameValue = *params.Rule
			}
		case *eventbridge.RemoveTargetsInput:
			if params.Rule != nil {
				ruleNameValue = *params.Rule
			}
	}
	
	return tagRuleName, ruleNameValue
}

func extractStateMachineName(requestInput middleware.InitializeInput) (resourceNameKey string, resourceNameValue string) {
	fmt.Println("got in extractStateMachineName")
	stateMachineNameValue := ""

	switch params := requestInput.Parameters.(type) {
		case *sfn.CreateStateMachineInput:
			if params.Name != nil {
				stateMachineNameValue = *params.Name
			}
		case *sfn.DescribeStateMachineInput:
			if params.StateMachineArn != nil {
				stateMachineArnValue := *params.StateMachineArn
				//arn:aws:states:us-east-1:123456789012:stateMachine:MyStateMachine
				parts := strings.Split(stateMachineArnValue, ":")
				stateMachineNameValue = parts[len(parts)-1]
			}
		case *sfn.StartExecutionInput:
			if params.StateMachineArn != nil {
				stateMachineArnValue := *params.StateMachineArn
				//arn:aws:states:us-east-1:123456789012:stateMachine:MyStateMachine
				parts := strings.Split(stateMachineArnValue, ":")
				stateMachineNameValue = parts[len(parts)-1]
			}
		case *sfn.StopExecutionInput:
			if params.ExecutionArn != nil {
				executionArnValue := *params.ExecutionArn
				//'arn:aws:states:us-east-1:123456789012:execution:example-state-machine:example-execution'
				parts := strings.Split(executionArnValue, ":")
				stateMachineNameValue = parts[len(parts)-2]
			}
		case *sfn.DescribeExecutionInput:
			if params.ExecutionArn != nil {
				executionArnValue := *params.ExecutionArn
				//'arn:aws:states:us-east-1:123456789012:execution:example-state-machine:example-execution'
				parts := strings.Split(executionArnValue, ":")
				stateMachineNameValue = parts[len(parts)-2]
			}
		case *sfn.ListExecutionsInput:
			if params.StateMachineArn != nil {
				stateMachineArnValue := *params.StateMachineArn
				//arn:aws:states:us-east-1:123456789012:stateMachine:MyStateMachine
				parts := strings.Split(stateMachineArnValue, ":")
				stateMachineNameValue = parts[len(parts)-1]
			}
		case *sfn.UpdateStateMachineInput:
			if params.StateMachineArn != nil {
				stateMachineArnValue := *params.StateMachineArn
				//arn:aws:states:us-east-1:123456789012:stateMachine:MyStateMachine
				parts := strings.Split(stateMachineArnValue, ":")
				stateMachineNameValue = parts[len(parts)-1]
			}
		case *sfn.DeleteStateMachineInput:
			if params.StateMachineArn != nil {
				stateMachineArnValue := *params.StateMachineArn
				//arn:aws:states:us-east-1:123456789012:stateMachine:MyStateMachine
				parts := strings.Split(stateMachineArnValue, ":")
				stateMachineNameValue = parts[len(parts)-1]
			}
	}	
	
	return tagStateMachineName, stateMachineNameValue
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
		fmt.Println("getting status code")
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
