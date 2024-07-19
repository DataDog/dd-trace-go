package namingschematest

import (
	"context"
	"os"
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
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func awsSDKV2Config(t *testing.T, opts ...awstrace.Option) aws.Config {
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
	awstrace.AppendMiddleware(&cfg, opts...)
	return cfg
}

var (
	awsSDKV2 = testCase{
		name: instrumentation.PackageAWSSDKGoV2,
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
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
		assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "EC2.request", spans[0].OperationName())
			assert.Equal(t, "S3.request", spans[1].OperationName())
			assert.Equal(t, "SQS.request", spans[2].OperationName())
			assert.Equal(t, "SNS.request", spans[3].OperationName())
		},
		assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "aws.ec2.request", spans[0].OperationName())
			assert.Equal(t, "aws.s3.request", spans[1].OperationName())
			assert.Equal(t, "aws.sqs.request", spans[2].OperationName())
			assert.Equal(t, "aws.sns.request", spans[3].OperationName())
		},
		wantServiceNameV0: serviceNameAssertions{
			defaults:        []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
			ddService:       []string{"aws.EC2", "aws.S3", "aws.SQS", "aws.SNS"},
			serviceOverride: []string{testServiceOverride, testServiceOverride, testServiceOverride, testServiceOverride},
		},
	}
	awsSDKV2Messaging = testCase{
		name: instrumentation.PackageAWSSDKGoV2 + "_messaging",
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
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
		assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 5)
			assert.Equal(t, "SQS.request", spans[0].OperationName())
			assert.Equal(t, "SQS.request", spans[1].OperationName())
			assert.Equal(t, "SQS.request", spans[2].OperationName())
			assert.Equal(t, "SNS.request", spans[3].OperationName())
			assert.Equal(t, "SNS.request", spans[4].OperationName())
		},
		assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 5)
			assert.Equal(t, "aws.sqs.request", spans[0].OperationName())
			assert.Equal(t, "aws.sqs.send", spans[1].OperationName())
			assert.Equal(t, "aws.sqs.send", spans[2].OperationName())
			assert.Equal(t, "aws.sns.request", spans[3].OperationName())
			assert.Equal(t, "aws.sns.send", spans[4].OperationName())
		},
		wantServiceNameV0: serviceNameAssertions{
			defaults:        []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
			ddService:       []string{"aws.SQS", "aws.SQS", "aws.SQS", "aws.SNS", "aws.SNS"},
			serviceOverride: repeatString(testServiceOverride, 5),
		},
	}
)
