// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	eventBridgeTypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendMiddleware(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked sqs failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sqs success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			sqsClient := sqs.NewFromConfig(awsCfg)
			// TODO(darccio): assert.NoError
			sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
				MessageBody: aws.String("foobar"),
				QueueUrl:    aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SQS.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "SendMessage", s.Tag("aws.operation"))
			assert.Equal(t, "SQS", s.Tag("aws.service"))
			assert.Equal(t, "SQS", s.Tag("aws_service"))
			assert.Equal(t, "MyQueueName", s.Tag("queuename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "SQS.SendMessage", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SQS", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareSqsDeleteMessage(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked sqs failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sqs success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
				QueueUrl:      aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
				ReceiptHandle: aws.String("foobar"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SQS.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "DeleteMessage", s.Tag("aws.operation"))
			assert.Equal(t, "SQS", s.Tag("aws.service"))
			assert.Equal(t, "SQS", s.Tag("aws_service"))
			assert.Equal(t, "MyQueueName", s.Tag("queuename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "SQS.DeleteMessage", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SQS", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareSqsReceiveMessage(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked sqs failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sqs success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
				QueueUrl: aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SQS.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "ReceiveMessage", s.Tag("aws.operation"))
			assert.Equal(t, "SQS", s.Tag("aws.service"))
			assert.Equal(t, "SQS", s.Tag("aws_service"))
			assert.Equal(t, "MyQueueName", s.Tag("queuename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "SQS", s.Tag("aws.service"))
			assert.Equal(t, "SQS.ReceiveMessage", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SQS", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareSqsSendMessage(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	expectedStatusCode := 200
	server := mockAWS(expectedStatusCode)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	sqsClient := sqs.NewFromConfig(awsCfg)
	sendMessageInput := &sqs.SendMessageInput{
		MessageBody: aws.String("test message"),
		QueueUrl:    aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
	}
	_, err := sqsClient.SendMessage(context.Background(), sendMessageInput)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s := spans[0]
	assert.Equal(t, "SQS.request", s.OperationName())
	assert.Equal(t, "SendMessage", s.Tag("aws.operation"))
	assert.Equal(t, "SQS", s.Tag("aws.service"))
	assert.Equal(t, "MyQueueName", s.Tag("queuename"))
	assert.Equal(t, "SQS.SendMessage", s.Tag(ext.ResourceName))
	assert.Equal(t, "aws.SQS", s.Tag(ext.ServiceName))

	// Check for trace context injection
	assert.NotNil(t, sendMessageInput.MessageAttributes)
	assert.Contains(t, sendMessageInput.MessageAttributes, "_datadog")
	ddAttr := sendMessageInput.MessageAttributes["_datadog"]
	assert.Equal(t, "String", *ddAttr.DataType)
	assert.NotEmpty(t, *ddAttr.StringValue)

	// Decode and verify the injected trace context
	var traceContext map[string]string
	err = json.Unmarshal([]byte(*ddAttr.StringValue), &traceContext)
	assert.NoError(t, err)
	assert.Contains(t, traceContext, "x-datadog-trace-id")
	assert.Contains(t, traceContext, "x-datadog-parent-id")
	assert.NotEmpty(t, traceContext["x-datadog-trace-id"])
	assert.NotEmpty(t, traceContext["x-datadog-parent-id"])
}

func TestAppendMiddlewareS3ListObjects(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked s3 failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked s3 success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			s3Client := s3.NewFromConfig(awsCfg)
			s3Client.ListObjects(context.Background(), &s3.ListObjectsInput{
				Bucket: aws.String("MyBucketName"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "S3.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "ListObjects", s.Tag("aws.operation"))
			assert.Equal(t, "S3", s.Tag("aws.service"))
			assert.Equal(t, "S3", s.Tag("aws_service"))
			assert.Equal(t, "MyBucketName", s.Tag("bucketname"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "S3.ListObjects", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.S3", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "GET", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/MyBucketName", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareSnsPublish(t *testing.T) {
	tests := []struct {
		name               string
		publishInput       *sns.PublishInput
		tagKey             string
		expectedTagValue   string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name: "test mocked sns failure request",
			publishInput: &sns.PublishInput{
				Message:  aws.String("Hello world!"),
				TopicArn: aws.String("arn:aws:sns:us-east-1:111111111111:MyTopicName"),
			},
			tagKey:             "topicname",
			expectedTagValue:   "MyTopicName",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name: "test mocked sns destination topic arn success request",
			publishInput: &sns.PublishInput{
				Message:  aws.String("Hello world!"),
				TopicArn: aws.String("arn:aws:sns:us-east-1:111111111111:MyTopicName"),
			},
			tagKey:             "topicname",
			expectedTagValue:   "MyTopicName",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
		{
			name: "test mocked sns destination target arn success request",
			publishInput: &sns.PublishInput{
				Message:   aws.String("Hello world!"),
				TargetArn: aws.String("arn:aws:sns:us-east-1:111111111111:MyTargetName"),
			},
			tagKey:             "targetname",
			expectedTagValue:   "MyTargetName",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			snsClient := sns.NewFromConfig(awsCfg)
			snsClient.Publish(context.Background(), tt.publishInput)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SNS.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "Publish", s.Tag("aws.operation"))
			assert.Equal(t, "SNS", s.Tag("aws.service"))
			assert.Equal(t, "SNS", s.Tag("aws_service"))
			assert.Equal(t, tt.expectedTagValue, s.Tag(tt.tagKey))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "SNS.Publish", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SNS", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())

			// Check for trace context injection
			assert.NotNil(t, tt.publishInput.MessageAttributes)
			assert.Contains(t, tt.publishInput.MessageAttributes, "_datadog")
			ddAttr := tt.publishInput.MessageAttributes["_datadog"]
			assert.Equal(t, "Binary", *ddAttr.DataType)
			assert.NotEmpty(t, ddAttr.BinaryValue)

			// Decode and verify the injected trace context
			var traceContext map[string]string
			err := json.Unmarshal(ddAttr.BinaryValue, &traceContext)
			assert.NoError(t, err)
			assert.Contains(t, traceContext, "x-datadog-trace-id")
			assert.Contains(t, traceContext, "x-datadog-parent-id")
			assert.NotEmpty(t, traceContext["x-datadog-trace-id"])
			assert.NotEmpty(t, traceContext["x-datadog-parent-id"])
		})
	}
}

func TestAppendMiddlewareDynamodbGetItem(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked dynamodb failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked dynamodb success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			dynamoClient := dynamodb.NewFromConfig(awsCfg)
			_, err := dynamoClient.Query(context.Background(), &dynamodb.QueryInput{
				TableName: aws.String("MyTableName"),
			})
			if tt.expectedStatusCode == 200 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "DynamoDB.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "Query", s.Tag("aws.operation"))
			assert.Equal(t, "DynamoDB", s.Tag("aws.service"))
			assert.Equal(t, "DynamoDB", s.Tag("aws_service"))
			assert.Equal(t, "MyTableName", s.Tag("tablename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "DynamoDB.Query", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.DynamoDB", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareKinesisPutRecord(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked kinesis failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked kinesis success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			kinesisClient := kinesis.NewFromConfig(awsCfg)
			kinesisClient.PutRecord(context.Background(), &kinesis.PutRecordInput{
				StreamName:   aws.String("my-kinesis-stream"),
				Data:         []byte("Hello, Kinesis!"),
				PartitionKey: aws.String("my-partition-key"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "Kinesis.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "PutRecord", s.Tag("aws.operation"))
			assert.Equal(t, "Kinesis", s.Tag("aws.service"))
			assert.Equal(t, "Kinesis", s.Tag("aws_service"))
			assert.Equal(t, "my-kinesis-stream", s.Tag("streamname"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "Kinesis.PutRecord", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.Kinesis", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareEventBridgePutRule(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked eventbridge failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked eventbridge success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			eventbridgeClient := eventbridge.NewFromConfig(awsCfg)
			eventbridgeClient.PutRule(context.Background(), &eventbridge.PutRuleInput{
				Name: aws.String("my-event-rule-name"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "EventBridge.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "PutRule", s.Tag("aws.operation"))
			assert.Equal(t, "EventBridge", s.Tag("aws.service"))
			assert.Equal(t, "EventBridge", s.Tag("aws_service"))
			assert.Equal(t, "my-event-rule-name", s.Tag("rulename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "EventBridge.PutRule", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.EventBridge", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddlewareEventBridgePutEvents(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	expectedStatusCode := 200
	server := mockAWS(expectedStatusCode)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	eventbridgeClient := eventbridge.NewFromConfig(awsCfg)
	putEventsInput := &eventbridge.PutEventsInput{
		Entries: []eventBridgeTypes.PutEventsRequestEntry{
			{
				EventBusName: aws.String("my-event-bus"),
				Detail:       aws.String(`{"key": "value"}`),
			},
		},
	}
	eventbridgeClient.PutEvents(context.Background(), putEventsInput)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s := spans[0]
	assert.Equal(t, "PutEvents", s.Tag("aws.operation"))
	assert.Equal(t, "EventBridge.PutEvents", s.Tag(ext.ResourceName))

	// Check for trace context injection
	assert.Len(t, putEventsInput.Entries, 1)
	entry := putEventsInput.Entries[0]
	var detail map[string]interface{}
	err := json.Unmarshal([]byte(*entry.Detail), &detail)
	assert.NoError(t, err)
	assert.Contains(t, detail, "_datadog")
	ddData, ok := detail["_datadog"].(map[string]interface{})
	assert.True(t, ok)
	assert.Contains(t, ddData, "x-datadog-start-time")
	assert.Contains(t, ddData, "x-datadog-resource-name")
	assert.Equal(t, "my-event-bus", ddData["x-datadog-resource-name"])
}

func TestAppendMiddlewareSfnDescribeStateMachine(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked sfn failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sfn success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			sfnClient := sfn.NewFromConfig(awsCfg)
			sfnClient.DescribeStateMachine(context.Background(), &sfn.DescribeStateMachineInput{
				StateMachineArn: aws.String("arn:aws:states:us-west-2:123456789012:stateMachine:HelloWorld-StateMachine"),
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SFN.request", s.OperationName())
			assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go-v2")
			assert.Equal(t, "DescribeStateMachine", s.Tag("aws.operation"))
			assert.Equal(t, "SFN", s.Tag("aws.service"))
			assert.Equal(t, "SFN", s.Tag("aws_service"))
			assert.Equal(t, "HelloWorld-StateMachine", s.Tag("statemachinename"))

			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, "eu-west-1", s.Tag("region"))
			assert.Equal(t, "SFN.DescribeStateMachine", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SFN", s.Tag(ext.ServiceName))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
			assert.Equal(t, componentName, s.Integration())
		})
	}
}

func TestAppendMiddleware_ChainTerminated(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	awsCfg := aws.Config{}

	AppendMiddleware(&awsCfg)

	s3Client := s3.NewFromConfig(awsCfg)
	stackFn := func(stack *middleware.Stack) error {
		return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("stop", func(
			ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler,
		) (
			out middleware.FinalizeOutput, metadata middleware.Metadata, err error,
		) {
			// Terminate the middleware chain by not calling the next handler
			out.Result = &s3.ListObjectsOutput{}
			return
		}), middleware.After)
	}
	s3Client.ListObjects(context.Background(), &s3.ListObjectsInput{
		Bucket: aws.String("MyBucketName"),
	}, s3.WithAPIOptions(stackFn))

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
}

func TestAppendMiddleware_InnerSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	server := mockAWS(200)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	s3Client := s3.NewFromConfig(awsCfg)
	stackFn := func(stack *middleware.Stack) error {
		return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("stop", func(
			ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler,
		) (
			out middleware.FinalizeOutput, metadata middleware.Metadata, err error,
		) {
			// Start a new child span
			span, ctx := tracer.StartSpanFromContext(ctx, "inner span")
			defer span.Finish()
			out, metadata, err = next.HandleFinalize(ctx, in)
			return
		}), middleware.After)
	}
	s3Client.ListObjects(context.Background(), &s3.ListObjectsInput{
		Bucket: aws.String("MyBucketName"),
	}, s3.WithAPIOptions(stackFn))

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
}

func TestAppendMiddleware_WithNoTracer(t *testing.T) {
	server := mockAWS(200)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	sqsClient := sqs.NewFromConfig(awsCfg)
	_, err := sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
	assert.NoError(t, err)

}

func mockAWS(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Amz-RequestId", "test_req")
			w.WriteHeader(statusCode)
			w.Write([]byte(`{}`))
		}))
}

func TestAppendMiddleware_WithOpts(t *testing.T) {
	tests := []struct {
		name                string
		opts                []Option
		expectedServiceName string
		expectedRate        interface{}
	}{
		{
			name:                "with defaults",
			opts:                nil,
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
		{
			name:                "with enabled",
			opts:                []Option{WithAnalytics(true)},
			expectedServiceName: "aws.SQS",
			expectedRate:        1.0,
		},
		{
			name:                "with disabled",
			opts:                []Option{WithAnalytics(false)},
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
		{
			name:                "with service name",
			opts:                []Option{WithService("TestName")},
			expectedServiceName: "TestName",
			expectedRate:        nil,
		},
		{
			name:                "with override",
			opts:                []Option{WithAnalyticsRate(0.23)},
			expectedServiceName: "aws.SQS",
			expectedRate:        0.23,
		},
		{
			name:                "with rate outside boundary",
			opts:                []Option{WithAnalyticsRate(1.5)},
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(200)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg, tt.opts...)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, tt.expectedServiceName, s.Tag(ext.ServiceName))
			assert.Equal(t, tt.expectedRate, s.Tag(ext.EventSampleRate))
		})
	}
}

func TestHTTPCredentials(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var auth string

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if enc, ok := r.Header["Authorization"]; ok {
				encoded := strings.TrimPrefix(enc[0], "Basic ")
				if b64, err := base64.StdEncoding.DecodeString(encoded); err == nil {
					auth = string(b64)
				}
			}

			w.Header().Set("X-Amz-RequestId", "test_req")
			w.WriteHeader(200)
			w.Write([]byte(`{}`))
		}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	u.User = url.UserPassword("myuser", "mypassword")

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           u.String(),
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	sqsClient := sqs.NewFromConfig(awsCfg)
	sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})

	spans := mt.FinishedSpans()

	s := spans[0]
	assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
	assert.NotContains(t, s.Tag(ext.HTTPURL), "mypassword")
	assert.NotContains(t, s.Tag(ext.HTTPURL), "myuser")
	// Make sure we haven't modified the outgoing request, and the server still
	// receives the auth request.
	assert.Equal(t, auth, "myuser:mypassword")
}

func TestWithErrorCheck(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		errExist bool
	}{
		{
			name:     "with defaults",
			opts:     nil,
			errExist: true,
		},
		{
			name: "with errCheck true",
			opts: []Option{WithErrorCheck(func(_ error) bool {
				return true
			})},
			errExist: true,
		}, {
			name: "with errCheck false",
			opts: []Option{WithErrorCheck(func(_ error) bool {
				return false
			})},
			errExist: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(400)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg, tt.opts...)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, tt.errExist, s.Tag(ext.ErrorMsg) != nil)
		})
	}
}

func TestStreamName(t *testing.T) {
	dummyName := `my-stream`
	dummyArn := `arn:aws:kinesis:us-east-1:111111111111:stream/` + dummyName

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{
			name:     "PutRecords with ARN",
			input:    &kinesis.PutRecordsInput{StreamARN: &dummyArn},
			expected: dummyName,
		},
		{
			name:     "PutRecords with Name",
			input:    &kinesis.PutRecordsInput{StreamName: &dummyName},
			expected: dummyName,
		},
		{
			name:     "PutRecords with both",
			input:    &kinesis.PutRecordsInput{StreamName: &dummyName, StreamARN: &dummyArn},
			expected: dummyName,
		},
		{
			name:     "PutRecord with Name",
			input:    &kinesis.PutRecordInput{StreamName: &dummyName},
			expected: dummyName,
		},
		{
			name:     "CreateStream",
			input:    &kinesis.CreateStreamInput{StreamName: &dummyName},
			expected: dummyName,
		},
		{
			name:     "CreateStream with nothing",
			input:    &kinesis.CreateStreamInput{},
			expected: "",
		},
		{
			name:     "GetRecords",
			input:    &kinesis.GetRecordsInput{StreamARN: &dummyArn},
			expected: dummyName,
		},
		{
			name:     "GetRecords with nothing",
			input:    &kinesis.GetRecordsInput{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := middleware.InitializeInput{
				Parameters: tt.input,
			}
			val := streamName(req)
			assert.Equal(t, tt.expected, val)
		})
	}
}
