// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go (https://github.com/aws/aws-sdk-go).
//
// Deprecated: The AWS SDK for Go v1 is deprecated. Please migrate to github.com/aws/aws-sdk-go-v2 and use the corresponding integration.
// This integration will be removed in a future release.
package aws // import "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/eventbridge"
	"github.com/aws/aws-sdk-go/service/kinesis"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/aws/aws-sdk-go/service/sqs"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "aws/aws-sdk-go/aws"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageAWSSDKGo)
}

const (
	// SendHandlerName is the name of the Datadog NamedHandler for the Send phase of an awsv1 request
	SendHandlerName = "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws/handlers.Send"
	// CompleteHandlerName is the name of the Datadog NamedHandler for the Complete phase of an awsv1 request
	CompleteHandlerName = "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws/handlers.Complete"
)

type handlers struct {
	cfg *config
}

// WrapSession wraps a session.Session, causing requests and responses to be traced.
func WrapSession(s *session.Session, opts ...Option) *session.Session {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/aws/aws-sdk-go/aws: Wrapping Session: %#v", cfg)
	h := &handlers{cfg: cfg}
	s = s.Copy()
	s.Handlers.Send.PushFrontNamed(request.NamedHandler{
		Name: SendHandlerName,
		Fn:   h.Send,
	})
	s.Handlers.Complete.PushBackNamed(request.NamedHandler{
		Name: CompleteHandlerName,
		Fn:   h.Complete,
	})
	return s
}

func (h *handlers) Send(req *request.Request) {
	if req.RetryCount != 0 {
		return
	}
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.HTTPRequest.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.

	region := awsRegion(req)

	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ServiceName(h.serviceName(req)),
		tracer.ResourceName(resourceName(req)),
		tracer.Tag(ext.AWSAgent, awsAgent(req)),
		tracer.Tag(ext.AWSOperation, awsOperation(req)),
		tracer.Tag(ext.AWSRegionLegacy, region),
		tracer.Tag(ext.AWSRegion, region),
		tracer.Tag(ext.AWSService, awsService(req)),
		tracer.Tag(ext.HTTPMethod, req.Operation.HTTPMethod),
		tracer.Tag(ext.HTTPURL, url.String()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
	for k, v := range extraTagsForService(req) {
		opts = append(opts, tracer.Tag(k, v))
	}
	if !math.IsNaN(h.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, h.cfg.analyticsRate))
	}
	_, ctx := tracer.StartSpanFromContext(req.Context(), spanName(req), opts...)
	req.SetContext(ctx)
}

func (h *handlers) Complete(req *request.Request) {
	span, ok := tracer.SpanFromContext(req.Context())
	if !ok {
		return
	}
	span.SetTag(ext.AWSRetryCount, req.RetryCount)
	span.SetTag(ext.AWSRequestID, req.RequestID)
	if req.HTTPResponse != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(req.HTTPResponse.StatusCode))
	}
	if req.Error != nil && (h.cfg.errCheck == nil || h.cfg.errCheck(req.Error)) {
		span.SetTag(ext.Error, req.Error)
	}
	span.Finish()
}

func (h *handlers) serviceName(req *request.Request) string {
	if h.cfg.serviceName != "" {
		return h.cfg.serviceName
	}
	return instr.ServiceName(
		instrumentation.ComponentDefault,
		instrumentation.OperationContext{
			ext.AWSService: awsService(req),
		},
	)
}

func spanName(req *request.Request) string {
	return instr.OperationName(
		instrumentation.ComponentDefault,
		instrumentation.OperationContext{
			ext.AWSService:   awsService(req),
			ext.AWSOperation: awsOperation(req),
		},
	)
}

func awsService(req *request.Request) string {
	return req.ClientInfo.ServiceName
}

func awsOperation(req *request.Request) string {
	return req.Operation.Name
}

func resourceName(req *request.Request) string {
	return awsService(req) + "." + awsOperation(req)
}

func awsAgent(req *request.Request) string {
	if agent := req.HTTPRequest.Header.Get("User-Agent"); agent != "" {
		return agent
	}
	return "aws-sdk-go"
}

func awsRegion(req *request.Request) string {
	return req.ClientInfo.SigningRegion
}

func extraTagsForService(req *request.Request) map[string]interface{} {
	service := awsService(req)
	var (
		extraTags map[string]interface{}
		err       error
	)
	switch service {
	case sqs.ServiceName:
		extraTags, err = sqsTags(req.Params)
	case s3.ServiceName:
		extraTags, err = s3Tags(req.Params)
	case sns.ServiceName:
		extraTags, err = snsTags(req.Params)
	case dynamodb.ServiceName:
		extraTags, err = dynamoDBTags(req.Params)
	case kinesis.ServiceName:
		extraTags, err = kinesisTags(req.Params)
	case eventbridge.ServiceName:
		extraTags, err = eventBridgeTags(req.Params)
	case sfn.ServiceName:
		extraTags, err = sfnTags(req.Params)
	default:
		return nil
	}
	if err != nil {
		instr.Logger().Debug("failed to extract tags for AWS service %q: %v", service, err)
		return nil
	}
	return extraTags
}

func sqsTags(params interface{}) (map[string]interface{}, error) {
	var queueURL string
	switch input := params.(type) {
	case *sqs.SendMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.DeleteMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.DeleteMessageBatchInput:
		queueURL = *input.QueueUrl
	case *sqs.ReceiveMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.SendMessageBatchInput:
		queueURL = *input.QueueUrl
	default:
		return nil, nil
	}
	parts := strings.Split(queueURL, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("got unexpected queue URL format: %q", queueURL)
	}
	queueName := parts[len(parts)-1]

	return map[string]interface{}{
		ext.SQSQueueName: queueName,
	}, nil
}

func s3Tags(params interface{}) (map[string]interface{}, error) {
	var bucket string
	switch input := params.(type) {
	case *s3.ListObjectsInput:
		bucket = *input.Bucket
	case *s3.ListObjectsV2Input:
		bucket = *input.Bucket
	case *s3.PutObjectInput:
		bucket = *input.Bucket
	case *s3.GetObjectInput:
		bucket = *input.Bucket
	case *s3.DeleteObjectInput:
		bucket = *input.Bucket
	case *s3.DeleteObjectsInput:
		bucket = *input.Bucket
	default:
		return nil, nil
	}
	return map[string]interface{}{
		ext.S3BucketName: bucket,
	}, nil
}

func snsTags(params interface{}) (map[string]interface{}, error) {
	var destTag, destName, destARN string
	switch input := params.(type) {
	case *sns.PublishInput:
		if input.TopicArn != nil {
			destTag, destARN = ext.SNSTopicName, *input.TopicArn
		} else {
			destTag, destARN = ext.SNSTargetName, *input.TargetArn
		}
	case *sns.GetTopicAttributesInput:
		destTag, destARN = ext.SNSTopicName, *input.TopicArn
	case *sns.ListSubscriptionsByTopicInput:
		destTag, destARN = ext.SNSTopicName, *input.TopicArn
	case *sns.RemovePermissionInput:
		destTag, destARN = ext.SNSTopicName, *input.TopicArn
	case *sns.SetTopicAttributesInput:
		destTag, destARN = ext.SNSTopicName, *input.TopicArn
	case *sns.SubscribeInput:
		destTag, destARN = ext.SNSTopicName, *input.TopicArn
	case *sns.CreateTopicInput:
		destTag, destName = ext.SNSTopicName, *input.Name
	default:
		return nil, nil
	}
	if destName == "" {
		parts := strings.Split(destARN, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("got unexpected ARN format: %q", destARN)
		}
		destName = parts[len(parts)-1]
	}
	return map[string]interface{}{
		destTag: destName,
	}, nil
}

func dynamoDBTags(params interface{}) (map[string]interface{}, error) {
	var tableName string
	switch input := params.(type) {
	case *dynamodb.GetItemInput:
		tableName = *input.TableName
	case *dynamodb.PutItemInput:
		tableName = *input.TableName
	case *dynamodb.QueryInput:
		tableName = *input.TableName
	case *dynamodb.ScanInput:
		tableName = *input.TableName
	case *dynamodb.UpdateItemInput:
		tableName = *input.TableName
	default:
		return nil, nil
	}
	return map[string]interface{}{
		ext.DynamoDBTableName: tableName,
	}, nil
}

func kinesisTags(params interface{}) (map[string]interface{}, error) {
	var streamName string
	switch input := params.(type) {
	case *kinesis.PutRecordInput:
		streamName = *input.StreamName
	case *kinesis.PutRecordsInput:
		streamName = *input.StreamName
	case *kinesis.AddTagsToStreamInput:
		streamName = *input.StreamName
	case *kinesis.RemoveTagsFromStreamInput:
		streamName = *input.StreamName
	case *kinesis.CreateStreamInput:
		streamName = *input.StreamName
	case *kinesis.DeleteStreamInput:
		streamName = *input.StreamName
	case *kinesis.DescribeStreamInput:
		streamName = *input.StreamName
	case *kinesis.DescribeStreamSummaryInput:
		streamName = *input.StreamName
	case *kinesis.GetShardIteratorInput:
		streamName = *input.StreamName
	default:
		return nil, nil
	}
	return map[string]interface{}{
		ext.KinesisStreamName: streamName,
	}, nil
}

func eventBridgeTags(params interface{}) (map[string]interface{}, error) {
	var ruleName string
	switch input := params.(type) {
	case *eventbridge.PutRuleInput:
		ruleName = *input.Name
	case *eventbridge.DescribeRuleInput:
		ruleName = *input.Name
	case *eventbridge.DeleteRuleInput:
		ruleName = *input.Name
	case *eventbridge.DisableRuleInput:
		ruleName = *input.Name
	case *eventbridge.EnableRuleInput:
		ruleName = *input.Name
	case *eventbridge.PutTargetsInput:
		ruleName = *input.Rule
	case *eventbridge.RemoveTargetsInput:
		ruleName = *input.Rule
	default:
		return nil, nil
	}
	return map[string]interface{}{
		ext.EventBridgeRuleName: ruleName,
	}, nil
}

func sfnTags(params interface{}) (map[string]interface{}, error) {
	var stateMachineName, stateMachineArn string
	switch input := params.(type) {
	case *sfn.CreateStateMachineInput:
		stateMachineName = *input.Name
	case *sfn.DescribeStateMachineInput:
		stateMachineArn = *input.StateMachineArn
	case *sfn.StartExecutionInput:
		stateMachineArn = *input.StateMachineArn
	case *sfn.StopExecutionInput:
		name, err := stateMachineNameFromExecutionARN(input.ExecutionArn)
		if err != nil {
			return nil, err
		}
		stateMachineName = name
	case *sfn.DescribeExecutionInput:
		name, err := stateMachineNameFromExecutionARN(input.ExecutionArn)
		if err != nil {
			return nil, err
		}
		stateMachineName = name
	case *sfn.ListExecutionsInput:
		stateMachineArn = *input.StateMachineArn
	case *sfn.UpdateStateMachineInput:
		stateMachineArn = *input.StateMachineArn
	case *sfn.DeleteStateMachineInput:
		stateMachineArn = *input.StateMachineArn
	}
	if stateMachineName == "" {
		parts := strings.Split(stateMachineArn, ":")
		stateMachineName = parts[len(parts)-1]
	}
	return map[string]interface{}{
		ext.SFNStateMachineName: stateMachineName,
	}, nil
}

// stateMachineNameFromExecutionARN returns the state machine name from the given execution ARN.
// The execution ARN should have a format like: arn:aws:states:us-east-1:123456789012:execution:stateMachineName:executionName
func stateMachineNameFromExecutionARN(arn *string) (string, error) {
	if arn == nil {
		return "", errors.New("got empty execution ARN")
	}
	parts := strings.Split(*arn, ":")
	if len(parts) < 3 {
		return "", fmt.Errorf("got unexpected execution ARN format: %q", *arn)
	}
	return parts[len(parts)-2], nil
}
