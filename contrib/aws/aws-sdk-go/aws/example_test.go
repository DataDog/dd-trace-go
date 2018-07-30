package aws_test

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"
)

// To start tracing requests, wrap the AWS session.Session by invoking
// awstrace.WrapSession
func Example() {
	cfg := aws.NewConfig().WithRegion("us-west-2")
	sess := session.Must(session.NewSession(cfg))
	sess = awstrace.WrapSession(sess)

	s3api := s3.New(sess)
	s3api.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String("some-bucket-name"),
	})
}
