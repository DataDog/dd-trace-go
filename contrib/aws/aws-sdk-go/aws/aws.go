// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go (https://github.com/aws/aws-sdk-go).
package aws // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"

import (
	"math"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/internal/awsnamingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/internal/tags"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
)

const componentName = "aws/aws-sdk-go/aws"

func init() {
	telemetry.LoadIntegration(componentName)
}

const (
	tagOldAWSRegion  = "aws.region"
	tagAWSRetryCount = "aws.retry_count"

	// SendHandlerName is the name of the Datadog NamedHandler for the Send phase of an awsv1 request
	SendHandlerName = "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws/handlers.Send"
	// CompleteHandlerName is the name of the Datadog NamedHandler for the Complete phase of an awsv1 request
	CompleteHandlerName = "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws/handlers.Complete"
)

type handlers struct {
	cfg *config
}

// WrapSession wraps a session.Session, causing requests and responses to be traced.
func WrapSession(s *session.Session, opts ...Option) *session.Session {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/aws/aws-sdk-go/aws: Wrapping Session: %#v", cfg)
	h := &handlers{cfg: cfg}
	s = s.Copy()
	s.Handlers.Send.PushFrontNamed(request.NamedHandler{
		Name: SendHandlerName,
		Fn:   h.Send,
	})
	s.Handlers.Complete.PushBackNamed(request.NamedHandler{
		Name: CompleteHandlerName,
		Fn:   h.Complete,
	})
	return s
}

func (h *handlers) Send(req *request.Request) {
	if req.RetryCount != 0 {
		return
	}
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.HTTPRequest.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.

	region := awsRegion(req)

	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ServiceName(h.serviceName(req)),
		tracer.ResourceName(resourceName(req)),
		tracer.Tag(tags.AWSAgent, awsAgent(req)),
		tracer.Tag(tags.AWSOperation, awsOperation(req)),
		tracer.Tag(tagOldAWSRegion, region),
		tracer.Tag(tags.AWSRegion, region),
		tracer.Tag(tags.AWSService, awsService(req)),
		tracer.Tag(ext.HTTPMethod, req.Operation.HTTPMethod),
		tracer.Tag(ext.HTTPURL, url.String()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
	for k, v := range extraTagsForService(req) {
		opts = append(opts, tracer.Tag(k, v))
	}
	if !math.IsNaN(h.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, h.cfg.analyticsRate))
	}
	_, ctx := tracer.StartSpanFromContext(req.Context(), spanName(req), opts...)
	req.SetContext(ctx)
}

func (h *handlers) Complete(req *request.Request) {
	span, ok := tracer.SpanFromContext(req.Context())
	if !ok {
		return
	}
	span.SetTag(tagAWSRetryCount, req.RetryCount)
	span.SetTag(tags.AWSRequestID, req.RequestID)
	if req.HTTPResponse != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(req.HTTPResponse.StatusCode))
	}
	if req.Error != nil && (h.cfg.errCheck == nil || h.cfg.errCheck(req.Error)) {
		span.SetTag(ext.Error, req.Error)
	}
	span.Finish()
}

func (h *handlers) serviceName(req *request.Request) string {
	if h.cfg.serviceName != "" {
		return h.cfg.serviceName
	}
	defaultName := "aws." + awsService(req)
	return namingschema.NewDefaultServiceName(
		defaultName,
		namingschema.WithOverrideV0(defaultName),
	).GetName()
}

func spanName(req *request.Request) string {
	svc := awsService(req)
	op := awsOperation(req)
	getSpanNameV0 := func(awsService string) string { return awsService + ".command" }
	return awsnamingschema.NewAWSOutboundOp(svc, op, getSpanNameV0).GetName()
}

func awsService(req *request.Request) string {
	return req.ClientInfo.ServiceName
}

func awsOperation(req *request.Request) string {
	return req.Operation.Name
}

func resourceName(req *request.Request) string {
	return awsService(req) + "." + awsOperation(req)
}

func awsAgent(req *request.Request) string {
	if agent := req.HTTPRequest.Header.Get("User-Agent"); agent != "" {
		return agent
	}
	return "aws-sdk-go"
}

func awsRegion(req *request.Request) string {
	return req.ClientInfo.SigningRegion
}

var serviceTags = map[string]func(params interface{}) (map[string]string, error){
	"sqs": sqsTags,
}

func extraTagsForService(req *request.Request) map[string]string {
	service := awsService(req)
	fn, ok := serviceTags[service]
	if !ok {
		return nil
	}
	r, err := fn(req.Params)
	if err != nil {
		log.Debug("failed to extract tags for AWS service %s: %v", service, err)
	}
	return r
}

func sqsTags(params interface{}) (map[string]string, error) {
	var queueURL string
	switch input := params.(type) {
	case *sqs.SendMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.DeleteMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.DeleteMessageBatchInput:
		queueURL = *input.QueueUrl
	case *sqs.ReceiveMessageInput:
		queueURL = *input.QueueUrl
	case *sqs.SendMessageBatchInput:
		queueURL = *input.QueueUrl
	}
	parts := strings.Split(queueURL, "/")
	queueName := parts[len(parts)-1]

	return map[string]string{
		tags.SQSQueueName: queueName,
	}, nil
}
