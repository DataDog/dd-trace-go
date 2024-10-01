package sqs

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	IntegrationName = "aws.sqs"
	datadogTraceKey = "_datadog"
)

type SendMessageAPIClient interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type messageCarrier map[string]string

func (c messageCarrier) Set(key, val string) {
	c[key] = val
}

func (c messageCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func InjectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return err
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return err
	}

	messageAttributes[datadogTraceKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}

	return nil
}
