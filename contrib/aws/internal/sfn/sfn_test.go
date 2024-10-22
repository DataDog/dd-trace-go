package sfn

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestEnrichOperation_StartExecution(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span := tracer.StartSpan("test")
	defer span.Finish()

	params := &sfn.StartExecutionInput{
		Input: aws.String(`{"key": "value"}`),
	}
	in := middleware.InitializeInput{
		Parameters: params,
	}

	EnrichOperation(span, in, "StartExecution")

	// Add assertions to verify the enriched input
}

func TestEnrichOperation_StartSyncExecution(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span := tracer.StartSpan("test")
	defer span.Finish()

	params := &sfn.StartSyncExecutionInput{
		Input: aws.String(`{"key": "value"}`),
	}
	in := middleware.InitializeInput{
		Parameters: params,
	}

	EnrichOperation(span, in, "StartSyncExecution")

	// TODO Dylan: Add assertions to verify the enriched input
}
