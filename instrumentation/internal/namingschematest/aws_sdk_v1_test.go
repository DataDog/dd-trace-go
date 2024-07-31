package namingschematest

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func awsSDKV1Session(opts ...awstrace.Option) *session.Session {
	cfg := aws.NewConfig().
		WithRegion("us-east-1").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials).
		WithEndpoint("http://localhost:4566"). // use localstack
		WithS3ForcePathStyle(true)

	return awstrace.WrapSession(session.Must(session.NewSession(cfg)), opts...)
}

var (
	awsSDKV1 = testCase{
		name: instrumentation.PackageAWSSDKGo,
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []awstrace.Option
			if serviceOverride != "" {
				opts = append(opts, awstrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			s := awsSDKV1Session(opts...)
			ec2Client := ec2.New(s)
			s3Client := s3.New(s)
			sqsClient := sqs.New(s)
			snsClient := sns.New(s)

			_, err := ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{})
			require.NoError(t, err)
			_, err = s3Client.ListBuckets(&s3.ListBucketsInput{})
			require.NoError(t, err)
			_, err = sqsClient.ListQueues(&sqs.ListQueuesInput{})
			require.NoError(t, err)
			_, err = snsClient.ListTopics(&sns.ListTopicsInput{})
			require.NoError(t, err)

			return mt.FinishedSpans()
		},
		wantServiceNameV0: serviceNameAssertions{
			defaults:        []string{"aws.ec2", "aws.s3", "aws.sqs", "aws.sns"},
			ddService:       []string{"aws.ec2", "aws.s3", "aws.sqs", "aws.sns"},
			serviceOverride: []string{testServiceOverride, testServiceOverride, testServiceOverride, testServiceOverride},
		},
		assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "ec2.command", spans[0].OperationName())
			assert.Equal(t, "s3.command", spans[1].OperationName())
			assert.Equal(t, "sqs.command", spans[2].OperationName())
			assert.Equal(t, "sns.command", spans[3].OperationName())
		},
		assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 4)
			assert.Equal(t, "aws.ec2.request", spans[0].OperationName())
			assert.Equal(t, "aws.s3.request", spans[1].OperationName())
			assert.Equal(t, "aws.sqs.request", spans[2].OperationName())
			assert.Equal(t, "aws.sns.request", spans[3].OperationName())
		},
	}

	awsSDKV1Messaging = testCase{
		name: instrumentation.PackageAWSSDKGo + "_messaging",
		genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
			var opts []awstrace.Option
			if serviceOverride != "" {
				opts = append(opts, awstrace.WithService(serviceOverride))
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			s := awsSDKV1Session(opts...)
			resourceName := "test-naming-schema-aws-v1"
			sqsClient := sqs.New(s)
			snsClient := sns.New(s)

			// create a SQS queue
			sqsResp, err := sqsClient.CreateQueue(&sqs.CreateQueueInput{QueueName: aws.String(resourceName)})
			require.NoError(t, err)

			msg := &sqs.SendMessageInput{QueueUrl: sqsResp.QueueUrl, MessageBody: aws.String("body")}
			_, err = sqsClient.SendMessage(msg)
			require.NoError(t, err)

			batchMsg := &sqs.SendMessageBatchInput{QueueUrl: sqsResp.QueueUrl}
			entry := &sqs.SendMessageBatchRequestEntry{Id: aws.String("1"), MessageBody: aws.String("body")}
			batchMsg.SetEntries([]*sqs.SendMessageBatchRequestEntry{entry})
			_, err = sqsClient.SendMessageBatch(batchMsg)
			require.NoError(t, err)

			// create an SNS topic
			snsResp, err := snsClient.CreateTopic(&sns.CreateTopicInput{Name: aws.String(resourceName)})
			require.NoError(t, err)

			_, err = snsClient.Publish(&sns.PublishInput{TopicArn: snsResp.TopicArn, Message: aws.String("message")})
			require.NoError(t, err)

			return mt.FinishedSpans()
		},
		assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 5)
			assert.Equal(t, "sqs.command", spans[0].OperationName())
			assert.Equal(t, "sqs.command", spans[1].OperationName())
			assert.Equal(t, "sqs.command", spans[2].OperationName())
			assert.Equal(t, "sns.command", spans[3].OperationName())
			assert.Equal(t, "sns.command", spans[4].OperationName())
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
			defaults:        []string{"aws.sqs", "aws.sqs", "aws.sqs", "aws.sns", "aws.sns"},
			ddService:       []string{"aws.sqs", "aws.sqs", "aws.sqs", "aws.sns", "aws.sns"},
			serviceOverride: repeatString(testServiceOverride, 5),
		},
	}
)
