// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws_test

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"

	awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

// To start tracing requests, wrap the AWS session.Session by invoking
// awstrace.WrapSession.
func Example() {
	cfg := aws.NewConfig().WithRegion("us-west-2")
	sess := session.Must(session.NewSession(cfg))
	sess = awstrace.WrapSession(sess)

	s3api := s3.New(sess)
	s3api.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String("some-bucket-name"),
	})
}

func TestWithErrorCheck(t *testing.T) {
	testOpts := func(errExist bool, opts ...awstrace.Option) func(t *testing.T) {
		return func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cfg := aws.NewConfig().WithRegion("us-west-2")
			sess := session.Must(session.NewSession(cfg))
			sess = awstrace.WrapSession(sess, opts...)

			s3api := s3.New(sess)
			s3api.CreateBucket(&s3.CreateBucketInput{
				Bucket: aws.String("some-bucket-name"),
			})

			spans := mt.FinishedSpans()
			assert.True(t, len(spans) > 0)

			s := spans[len(spans)-1]
			assert.Equal(t, errExist, s.Tag(ext.Error) != nil)
		}
	}

	t.Run("defaults", testOpts(true))
	t.Run("errcheck", testOpts(false, awstrace.WithErrorCheck(func(err error) bool {
		return !strings.Contains(err.Error(), `InvalidAccessKeyId: The AWS Access Key Id you provided does not exist in our records`)
	})))

}
