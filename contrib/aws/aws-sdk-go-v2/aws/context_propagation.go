package aws

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	datadogKey = "_datadog"
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

//func (carrier messageCarrier) ForeachKey(handler func(key, val string) error) error {
//	for k, v := range carrier {
//		if err := handler(k, v); err != nil {
//			return err
//		}
//	}
//	return nil
//}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) error {
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

	messageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}

	return nil
}
