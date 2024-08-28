// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func awsSDKV2Config(t *testing.T, opts ...awstrace.Option) aws.Config {
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
	awstrace.AppendMiddleware(&cfg, opts...)
	return cfg
}

var (
	awsSDKV2 = harness.TestCase{
		Name: instrumentation.PackageAWSSDKGoV2,
		GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []awstrace.Option
			if serviceOverride != "" {
				opts = append(opts, awstrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			awsCfg := awsSDKV2Config(t, opts...)
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
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "EC2.request", spans[0].OperationName())
			assert.Equal(t, "S3.request", spans[1].OperationName())
			assert.Equal(t, "SQS.request", spans[2].OperationName())
			assert.Equal(t, "SNS.request", spans[3].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "aws.ec2.request", spans[0].OperationName())
			assert.Equal(t, "aws.s3.request", spans[1].OperationName())
			assert.Equal(t, "aws.sqs.request", spans[2].OperationName())
			assert.Equal(t, "aws.sns.request", spans[3].OperationName())
		},
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
			DDService:       []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
			ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride, harness.TestServiceOverride, harness.TestServiceOverride},
		},
	}
	awsSDKV2Messaging = harness.TestCase{
		Name: instrumentation.PackageAWSSDKGoV2 + "_messaging",
		GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []awstrace.Option
			if serviceOverride != "" {
				opts = append(opts, awstrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			awsCfg := awsSDKV2Config(t, opts...)
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
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 5)
			assert.Equal(t, "SQS.request", spans[0].OperationName())
			assert.Equal(t, "SQS.request", spans[1].OperationName())
			assert.Equal(t, "SQS.request", spans[2].OperationName())
			assert.Equal(t, "SNS.request", spans[3].OperationName())
			assert.Equal(t, "SNS.request", spans[4].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 5)
			assert.Equal(t, "aws.sqs.request", spans[0].OperationName())
			assert.Equal(t, "aws.sqs.send", spans[1].OperationName())
			assert.Equal(t, "aws.sqs.send", spans[2].OperationName())
			assert.Equal(t, "aws.sns.request", spans[3].OperationName())
			assert.Equal(t, "aws.sns.send", spans[4].OperationName())
		},
		WantServiceNameV0: harness.ServiceNameAssertions{
			Defaults:        []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
			DDService:       []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
			ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 5),
		},
	}
)
