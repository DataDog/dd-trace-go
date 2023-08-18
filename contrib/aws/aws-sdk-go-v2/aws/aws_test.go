// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIntegrationTestConfig(t *testing.T, opts ...Option) aws.Config {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
	awsEndpoint := "http://localhost:4566" // use localstack
	awsRegion := "us-east-1"

	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           awsEndpoint,
			SigningRegion: awsRegion,
		}, nil
	})
	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion(awsRegion),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
		awsconfig.WithCredentialsProvider(aws.AnonymousCredentials{}),
	)
	require.NoError(t, err, "failed to load AWS config")
	AppendMiddleware(&cfg, opts...)
	return cfg
}

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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
				MessageBody: aws.String("foobar"),
				QueueUrl:    aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
			})

			spans := mt.FinishedSpans()

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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		})
	}
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "GET", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/MyBucketName", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			dynamoClient.Query(context.Background(), &dynamodb.QueryInput{
				TableName: aws.String("MyTableName"),
			})

			spans := mt.FinishedSpans()

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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		})
	}
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		})
	}
}

func TestAppendMiddleware_WithNoTracer(t *testing.T) {
	server := mockAWS(200)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
		func(w http.ResponseWriter, r *http.Request) {
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
			opts:                []Option{WithServiceName("TestName")},
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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

	resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		awsCfg := newIntegrationTestConfig(t, opts...)
		ctx := context.Background()
		ec2Client := ec2.NewFromConfig(awsCfg)
		s3Client := s3.NewFromConfig(awsCfg)
		sqsClient := sqs.NewFromConfig(awsCfg)
		snsClient := sns.NewFromConfig(awsCfg)

		_, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
		require.NoError(t, err)
		_, err = s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})
		require.NoError(t, err)
		_, err = sqsClient.ListQueues(ctx, &sqs.ListQueuesInput{})
		require.NoError(t, err)
		_, err = snsClient.ListTopics(ctx, &sns.ListTopicsInput{})
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		assert.Equal(t, "EC2.request", spans[0].OperationName())
		assert.Equal(t, "S3.request", spans[1].OperationName())
		assert.Equal(t, "SQS.request", spans[2].OperationName())
		assert.Equal(t, "SNS.request", spans[3].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		assert.Equal(t, "aws.ec2.request", spans[0].OperationName())
		assert.Equal(t, "aws.s3.request", spans[1].OperationName())
		assert.Equal(t, "aws.sqs.request", spans[2].OperationName())
		assert.Equal(t, "aws.sns.request", spans[3].OperationName())
	}
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
		WithDDService:            []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
		WithDDServiceAndOverride: []string{serviceOverride, serviceOverride, serviceOverride, serviceOverride},
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}

func TestMessagingNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		awsCfg := newIntegrationTestConfig(t, opts...)
		resourceName := "test-naming-schema-aws-v2"
		ctx := context.Background()
		sqsClient := sqs.NewFromConfig(awsCfg)
		snsClient := sns.NewFromConfig(awsCfg)

		// create a SQS queue
		sqsResp, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(resourceName)})
		require.NoError(t, err)

		msg := &sqs.SendMessageInput{QueueUrl: sqsResp.QueueUrl, MessageBody: aws.String("body")}
		_, err = sqsClient.SendMessage(ctx, msg)
		require.NoError(t, err)

		entry := types.SendMessageBatchRequestEntry{Id: aws.String("1"), MessageBody: aws.String("body")}
		batchMsg := &sqs.SendMessageBatchInput{QueueUrl: sqsResp.QueueUrl, Entries: []types.SendMessageBatchRequestEntry{entry}}
		_, err = sqsClient.SendMessageBatch(ctx, batchMsg)
		require.NoError(t, err)

		// create an SNS topic
		snsResp, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(resourceName)})
		require.NoError(t, err)

		_, err = snsClient.Publish(ctx, &sns.PublishInput{TopicArn: snsResp.TopicArn, Message: aws.String("message")})
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 5)
		assert.Equal(t, "SQS.request", spans[0].OperationName())
		assert.Equal(t, "SQS.request", spans[1].OperationName())
		assert.Equal(t, "SQS.request", spans[2].OperationName())
		assert.Equal(t, "SNS.request", spans[3].OperationName())
		assert.Equal(t, "SNS.request", spans[4].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 5)
		assert.Equal(t, "aws.sqs.request", spans[0].OperationName())
		assert.Equal(t, "aws.sqs.send", spans[1].OperationName())
		assert.Equal(t, "aws.sqs.send", spans[2].OperationName())
		assert.Equal(t, "aws.sns.request", spans[3].OperationName())
		assert.Equal(t, "aws.sns.send", spans[4].OperationName())
	}
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
		WithDDService:            []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
		WithDDServiceAndOverride: repeat(serviceOverride, 5),
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}

func repeat(s string, n int) []string {
	r := make([]string, n)
	for i := 0; i < n; i++ {
		r[i] = s
	}
	return r
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
			opts: []Option{WithErrorCheck(func(err error) bool {
				return true
			})},
			errExist: true,
		}, {
			name: "with errCheck false",
			opts: []Option{WithErrorCheck(func(err error) bool {
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
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
			assert.Equal(t, tt.errExist, s.Tag(ext.Error) != nil)
		})
	}
}
