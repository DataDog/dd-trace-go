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
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
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
		assert.Contains(t, s.Tag(tagAWSAgent), "aws-sdk-go")
		assert.Equal(t, "CreateBucket", s.Tag(tagAWSOperation))
		assert.Equal(t, "us-west-2", s.Tag(tagAWSRegion))
		assert.Equal(t, "s3.CreateBucket", s.Tag(ext.ResourceName))
		assert.Equal(t, "aws.s3", s.Tag(ext.ServiceName))
		assert.Equal(t, "403", s.Tag(ext.HTTPCode))
		assert.Equal(t, "PUT", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "http://s3.us-west-2.amazonaws.com/BUCKET", s.Tag(ext.HTTPURL))
		assert.Equal(t, "aws/aws-sdk-go/aws", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		assert.NotNil(t, s.Tag(tagAWSRequestID))
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
		assert.Contains(t, s.Tag(tagAWSAgent), "aws-sdk-go")
		assert.Equal(t, "DescribeInstances", s.Tag(tagAWSOperation))
		assert.Equal(t, "us-west-2", s.Tag(tagAWSRegion))
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
	assert.Equal(t, mt.FinishedSpans()[0].Tag(tagAWSRetryCount), 3)
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
