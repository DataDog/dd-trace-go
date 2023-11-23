// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/eventbridge"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/kinesis"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIntegrationTestSession(t *testing.T, opts ...Option) *session.Session {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
	cfg := aws.NewConfig().
		WithRegion("us-east-1").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials).
		WithEndpoint("http://localhost:4566"). // use localstack
		WithS3ForcePathStyle(true)

	return WrapSession(session.Must(session.NewSession(cfg)), opts...)
}

func TestAWS(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := WrapSession(session.Must(session.NewSession(cfg)))

	t.Run("s3", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		root, ctx := tracer.StartSpanFromContext(context.Background(), "test")
		s3api := s3.New(session)
		s3api.CreateBucketWithContext(ctx, &s3.CreateBucketInput{
			Bucket: aws.String("BUCKET"),
		})
		root.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())

		s := spans[0]
		assert.Equal(t, "s3.command", s.OperationName())
		assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go")
		assert.Equal(t, "CreateBucket", s.Tag("aws.operation"))
		assert.Equal(t, "us-west-2", s.Tag("aws.region"))
		assert.Equal(t, "s3.CreateBucket", s.Tag(ext.ResourceName))
		assert.Equal(t, "aws.s3", s.Tag(ext.ServiceName))
		assert.Equal(t, "403", s.Tag(ext.HTTPCode))
		assert.Equal(t, "PUT", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "http://s3.us-west-2.amazonaws.com/BUCKET", s.Tag(ext.HTTPURL))
		assert.Equal(t, "aws/aws-sdk-go/aws", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		assert.NotNil(t, s.Tag("aws.request_id"))
	})

	t.Run("ec2", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		root, ctx := tracer.StartSpanFromContext(context.Background(), "test")
		ec2api := ec2.New(session)
		ec2api.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{})
		root.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())

		s := spans[0]
		assert.Equal(t, "ec2.command", s.OperationName())
		assert.Contains(t, s.Tag("aws.agent"), "aws-sdk-go")
		assert.Equal(t, "DescribeInstances", s.Tag("aws.operation"))
		assert.Equal(t, "us-west-2", s.Tag("aws.region"))
		assert.Equal(t, "ec2.DescribeInstances", s.Tag(ext.ResourceName))
		assert.Equal(t, "aws.ec2", s.Tag(ext.ServiceName))
		assert.Equal(t, "400", s.Tag(ext.HTTPCode))
		assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "http://ec2.us-west-2.amazonaws.com/", s.Tag(ext.HTTPURL))
		assert.Equal(t, "aws/aws-sdk-go/aws", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := session.Must(session.NewSession(cfg))
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		ws := WrapSession(session, opts...)
		ec2.New(ws).DescribeInstancesWithContext(context.TODO(), &ec2.DescribeInstancesInput{})
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestRetries(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := WrapSession(session.Must(session.NewSession(cfg)))
	expectedError := errors.New("an error")
	session.Handlers.Send.PushBack(func(r *request.Request) {
		r.Error = expectedError
		r.Retryable = aws.Bool(true)
	})

	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	s3api := s3.New(session)
	req, _ := s3api.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String("BUCKET"),
		Key:    aws.String("KEY"),
	})
	req.SetContext(ctx)
	err := req.Send()

	assert.Equal(t, 3, req.RetryCount)
	assert.Same(t, expectedError, err)
	assert.Len(t, mt.OpenSpans(), 0)
	assert.Len(t, mt.FinishedSpans(), 1)
	assert.Equal(t, mt.FinishedSpans()[0].Tag("aws.retry_count"), 3)
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

	resolver := endpoints.ResolverFunc(func(service, region string, opts ...func(*endpoints.Options)) (endpoints.ResolvedEndpoint, error) {
		return endpoints.ResolvedEndpoint{
			PartitionID:   "aws",
			URL:           u.String(),
			SigningRegion: "eu-west-1",
		}, nil
	})

	region := "eu-west-1"
	awsCfg := aws.Config{
		Region:           &region,
		Credentials:      credentials.AnonymousCredentials,
		EndpointResolver: resolver,
	}
	session := WrapSession(session.Must(session.NewSession(&awsCfg)))

	ctx := context.Background()
	s3api := s3.New(session)
	req, _ := s3api.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String("BUCKET"),
		Key:    aws.String("KEY"),
	})
	req.SetContext(ctx)
	err = req.Send()
	require.NoError(t, err)

	spans := mt.FinishedSpans()

	s := spans[0]
	assert.Equal(t, server.URL+"/BUCKET/KEY", s.Tag(ext.HTTPURL))
	assert.NotContains(t, s.Tag(ext.HTTPURL), "mypassword")
	assert.NotContains(t, s.Tag(ext.HTTPURL), "myuser")
	// Make sure we haven't modified the outgoing request, and the server still
	// receives the auth request.
	assert.Equal(t, auth, "myuser:mypassword")
}

func TestWithErrorCheck(t *testing.T) {
	testOpts := func(errExist bool, opts ...Option) func(t *testing.T) {
		return func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			root, ctx := tracer.StartSpanFromContext(context.Background(), "test")
			sess := session.Must(session.NewSession(aws.NewConfig().WithRegion("us-west-2")))
			sess = WrapSession(sess, opts...)
			s3api := s3.New(sess)
			s3api.CreateBucketWithContext(ctx, &s3.CreateBucketInput{
				Bucket: aws.String("some-bucket-name"),
			})
			root.Finish()

			spans := mt.FinishedSpans()
			assert.True(t, len(spans) > 0)
			assert.Equal(t, errExist, spans[0].Tag(ext.Error) != nil)
		}
	}

	t.Run("defaults", testOpts(true))
	t.Run("errcheck", testOpts(false, WithErrorCheck(func(err error) bool {
		return false
	})))
}

func TestExtraTagsForService(t *testing.T) {
	const (
		sqsQueueName        = "test-queue-name"
		s3BucketName        = "test-bucket-name"
		kinesisStreamName   = "test-stream-name"
		dynamoDBTableName   = "test-table-name"
		snsTopicName        = "test-topic-name"
		eventBridgeRuleName = "test-rule-name"
		sfnStateMachineName = "test-state-machine-name"
	)
	sess := newIntegrationTestSession(t)

	sqsQueueURL := prepareSQS(t, sess, sqsQueueName)
	prepareS3(t, sess, s3BucketName)
	snsTopicARN := prepareSNS(t, sess, snsTopicName)
	prepareDynamoDB(t, sess, dynamoDBTableName)
	prepareKinesis(t, sess, kinesisStreamName)
	roleARN := prepareTestRole(t, sess)

	t.Run("SQS", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := sqs.New(sess)
		input := &sqs.SendMessageInput{QueueUrl: aws.String(sqsQueueURL), MessageBody: aws.String("body")}
		_, err := c.SendMessage(input)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "sqs", s0.Tag("aws_service"))
		assert.Equal(t, "SendMessage", s0.Tag("aws.operation"))
		assert.Equal(t, sqsQueueName, s0.Tag("queuename"))
	})
	t.Run("S3", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := s3.New(sess)
		_, err := c.ListObjects(&s3.ListObjectsInput{Bucket: aws.String(s3BucketName)})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "s3", s0.Tag("aws_service"))
		assert.Equal(t, "ListObjects", s0.Tag("aws.operation"))
		assert.Equal(t, s3BucketName, s0.Tag("bucketname"))
	})
	t.Run("SNS", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := sns.New(sess)
		_, err := c.Publish(&sns.PublishInput{TopicArn: aws.String(snsTopicARN), Message: aws.String("message")})
		require.NoError(t, err)
		_, err = c.Publish(&sns.PublishInput{TargetArn: aws.String(snsTopicARN), Message: aws.String("message")})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)

		s0 := spans[0]
		assert.Equal(t, "sns", s0.Tag("aws_service"))
		assert.Equal(t, "Publish", s0.Tag("aws.operation"))
		assert.Equal(t, snsTopicName, s0.Tag("topicname"))

		s1 := spans[1]
		assert.Equal(t, "sns", s1.Tag("aws_service"))
		assert.Equal(t, "Publish", s1.Tag("aws.operation"))
		assert.Equal(t, snsTopicName, s1.Tag("targetname"))
	})
	t.Run("DynamoDB", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := dynamodb.New(sess)
		_, err := c.GetItem(&dynamodb.GetItemInput{
			TableName: aws.String(dynamoDBTableName),
			Key: map[string]*dynamodb.AttributeValue{
				"Key": {S: aws.String("something")},
			}})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "dynamodb", s0.Tag("aws_service"))
		assert.Equal(t, "GetItem", s0.Tag("aws.operation"))
		assert.Equal(t, dynamoDBTableName, s0.Tag("tablename"))
	})
	t.Run("Kinesis", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := kinesis.New(sess)
		_, err := c.PutRecord(&kinesis.PutRecordInput{
			StreamName:   aws.String(kinesisStreamName),
			Data:         []byte("data"),
			PartitionKey: aws.String("1"),
		})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "kinesis", s0.Tag("aws_service"))
		assert.Equal(t, "PutRecord", s0.Tag("aws.operation"))
		assert.Equal(t, kinesisStreamName, s0.Tag("streamname"))
	})
	t.Run("EventBridge", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := eventbridge.New(sess)
		_, err := c.PutRule(&eventbridge.PutRuleInput{
			Name:         aws.String(eventBridgeRuleName),
			EventPattern: aws.String("{ \"source\": [\"aws.ec2\"] }"),
		})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "EventBridge", s0.Tag("aws_service"))
		assert.Equal(t, "PutRule", s0.Tag("aws.operation"))
		assert.Equal(t, eventBridgeRuleName, s0.Tag("rulename"))
	})
	t.Run("SFN", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := sfn.New(sess)
		definition := `{
  "Comment": "A Hello World example of the Amazon States Language using a Pass state",
  "StartAt": "HelloWorld",
  "States": {
    "HelloWorld": {
      "Type": "Pass",
      "Result": "Hello World!",
      "End": true
    }
  }
}`
		resp, err := c.CreateStateMachine(&sfn.CreateStateMachineInput{
			Name:       aws.String(sfnStateMachineName),
			Definition: aws.String(definition),
			RoleArn:    aws.String(roleARN),
		})
		require.NoError(t, err)
		_, err = c.DescribeStateMachine(&sfn.DescribeStateMachineInput{StateMachineArn: resp.StateMachineArn})
		require.NoError(t, err)
		execResp, err := c.StartExecution(&sfn.StartExecutionInput{StateMachineArn: resp.StateMachineArn})
		require.NoError(t, err)
		_, err = c.DescribeExecution(&sfn.DescribeExecutionInput{ExecutionArn: execResp.ExecutionArn})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 4)

		s0 := spans[0]
		assert.Equal(t, "states", s0.Tag("aws_service"))
		assert.Equal(t, "CreateStateMachine", s0.Tag("aws.operation"))
		assert.Equal(t, sfnStateMachineName, s0.Tag("statemachinename"))

		s1 := spans[1]
		assert.Equal(t, "states", s1.Tag("aws_service"))
		assert.Equal(t, "DescribeStateMachine", s1.Tag("aws.operation"))
		assert.Equal(t, sfnStateMachineName, s1.Tag("statemachinename"))

		s2 := spans[2]
		assert.Equal(t, "states", s2.Tag("aws_service"))
		assert.Equal(t, "StartExecution", s2.Tag("aws.operation"))
		assert.Equal(t, sfnStateMachineName, s2.Tag("statemachinename"))

		span3 := spans[3]
		assert.Equal(t, "states", span3.Tag("aws_service"))
		assert.Equal(t, "DescribeExecution", span3.Tag("aws.operation"))
		assert.Equal(t, sfnStateMachineName, span3.Tag("statemachinename"))
	})
	t.Run("EC2", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		c := ec2.New(sess)
		_, err := c.DescribeInstances(&ec2.DescribeInstancesInput{})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "ec2", s0.Tag("aws_service"))
		assert.Equal(t, "DescribeInstances", s0.Tag("aws.operation"))
		// no extra tags set at this point for EC2
	})
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		s := newIntegrationTestSession(t, opts...)
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
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 4)
		assert.Equal(t, "ec2.command", spans[0].OperationName())
		assert.Equal(t, "s3.command", spans[1].OperationName())
		assert.Equal(t, "sqs.command", spans[2].OperationName())
		assert.Equal(t, "sns.command", spans[3].OperationName())
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
		WithDefaults:             []string{"aws.ec2", "aws.s3", "aws.sqs", "aws.sns"},
		WithDDService:            []string{"aws.ec2", "aws.s3", "aws.sqs", "aws.sns"},
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

		s := newIntegrationTestSession(t, opts...)
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
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 5)
		assert.Equal(t, "sqs.command", spans[0].OperationName())
		assert.Equal(t, "sqs.command", spans[1].OperationName())
		assert.Equal(t, "sqs.command", spans[2].OperationName())
		assert.Equal(t, "sns.command", spans[3].OperationName())
		assert.Equal(t, "sns.command", spans[4].OperationName())
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
		WithDefaults:             []string{"aws.sqs", "aws.sqs", "aws.sqs", "aws.sns", "aws.sns"},
		WithDDService:            []string{"aws.sqs", "aws.sqs", "aws.sqs", "aws.sns", "aws.sns"},
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

func prepareSQS(t *testing.T, sess *session.Session, queueName string) string {
	t.Helper()
	c := sqs.New(sess)

	resp, err := c.CreateQueue(&sqs.CreateQueueInput{QueueName: aws.String(queueName)})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteQueue(&sqs.DeleteQueueInput{QueueUrl: resp.QueueUrl})
		assert.NoError(t, err)
	})
	return *resp.QueueUrl
}

func prepareS3(t *testing.T, sess *session.Session, bucketName string) {
	t.Helper()
	c := s3.New(sess)

	_, err := c.CreateBucket(&s3.CreateBucketInput{Bucket: aws.String(bucketName)})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteBucket(&s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
		assert.NoError(t, err)
	})
}

func prepareSNS(t *testing.T, sess *session.Session, topicName string) string {
	t.Helper()
	c := sns.New(sess)

	resp, err := c.CreateTopic(&sns.CreateTopicInput{Name: aws.String(topicName)})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteTopic(&sns.DeleteTopicInput{TopicArn: resp.TopicArn})
		assert.NoError(t, err)
	})
	return *resp.TopicArn
}

func prepareDynamoDB(t *testing.T, sess *session.Session, tableName string) {
	t.Helper()
	c := dynamodb.New(sess)

	_, err := c.CreateTable(&dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []*dynamodb.AttributeDefinition{
			{AttributeName: aws.String("Key"), AttributeType: aws.String("S")},
		},
		KeySchema: []*dynamodb.KeySchemaElement{
			{AttributeName: aws.String("Key"), KeyType: aws.String("HASH")},
		},
		BillingMode: aws.String("PAY_PER_REQUEST"),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteTable(&dynamodb.DeleteTableInput{TableName: aws.String(tableName)})
		assert.NoError(t, err)
	})
}

func prepareKinesis(t *testing.T, sess *session.Session, streamName string) {
	t.Helper()
	c := kinesis.New(sess)

	_, err := c.CreateStream(&kinesis.CreateStreamInput{
		StreamName: aws.String(streamName),
		ShardCount: aws.Int64(1),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteStream(&kinesis.DeleteStreamInput{StreamName: aws.String(streamName)})
		assert.NoError(t, err)
	})
	timeoutChan := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		resp, err := c.DescribeStream(&kinesis.DescribeStreamInput{StreamName: aws.String(streamName)})
		require.NoError(t, err)
		if *resp.StreamDescription.StreamStatus == "ACTIVE" {
			return
		}
		select {
		case <-ticker.C:
			continue
		case <-timeoutChan:
			assert.FailNow(t, "timeout waiting for kinesis stream to be ready")
		}
	}
}

func prepareTestRole(t *testing.T, sess *session.Session) string {
	t.Helper()
	c := iam.New(sess)
	roleName := "test-role"
	rolePolicyDoc := `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Principal": {
				"Service": [
					"ec2.amazonaws.com"
				]
			},
			"Action": [
				"sts:AssumeRole"
			]
		}
	]
}`
	resp, err := c.CreateRole(&iam.CreateRoleInput{
		RoleName:                 aws.String("test-role"),
		AssumeRolePolicyDocument: aws.String(rolePolicyDoc),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := c.DeleteRole(&iam.DeleteRoleInput{RoleName: aws.String(roleName)})
		assert.NoError(t, err)
	})
	return *resp.Role.Arn
}
