// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// These constants are intended to be used by tracers to extract and infer
// parent span information for distributed tracing systems.
const (
	// ProxyHeaderSystem is the header used to indicate the source of the
	// proxy. In the case of AWS API Gateway, the value of this header
	// will always be 'aws-apigateway'.
	ProxyHeaderSystem = "X-Dd-Proxy"

	// ProxyHeaderStartTimeMs is the header used to indicate the start time
	// of the request in milliseconds. This value corresponds to the
	// 'context.requestTimeEpoch' in AWS API Gateway, providing a timestamp
	// for when the request was initiated.
	ProxyHeaderStartTimeMs = "X-Dd-Proxy-Request-Time-Ms"

	// ProxyHeaderPath is the header used to indicate the path of the
	// request. This value corresponds to 'context.path' in AWS API Gateway,
	// and helps identify the resource that the request is targeting.
	ProxyHeaderPath = "X-Dd-Proxy-Path"

	// ProxyHeaderHTTPMethod is the header used to indicate the HTTP method
	// of the request (e.g., GET, POST, PUT, DELETE). This value corresponds
	// to 'context.httpMethod' in AWS API Gateway, and provides the method
	// used to make the request.
	ProxyHeaderHTTPMethod = "X-Dd-Proxy-Httpmethod"

	// ProxyHeaderDomain is the header used to indicate the AWS domain name
	// handling the request. This value corresponds to 'context.domainName'
	// in AWS API Gateway, which represents the custom domain associated
	// with the API Gateway.
	ProxyHeaderDomain = "X-Dd-Proxy-Domain-Name"

	// ProxyHeaderStage is the header used to indicate the AWS stage name
	// for the API request. This value corresponds to 'context.stage' in
	// AWS API Gateway, and provides the stage (e.g., dev, prod, etc.)
	// in which the request is being processed.
	ProxyHeaderStage = "X-Dd-Proxy-Stage"

	// PubsubHeaderSubscriptionName is the header gcp Pub/Sub sets on push deliveries when
	// write metadata is enabled. The value is the full subscription resource name
	// (projects/{project_id}/subscriptions/{subscription_id}).
	PubsubHeaderSubscriptionName = "X-Goog-Pubsub-Subscription-Name"

	// PubsubHeaderMessageID is the header gcp Pub/Sub sets on push deliveries when write
	// metadata is enabled. The value is the unique ID of the delivered message.
	PubsubHeaderMessageID = "X-Goog-Pubsub-Message-Id"
)

type proxyDetails struct {
	spanName  string
	component string
}

type proxyContext struct {
	startTime       time.Time
	method          string
	path            string
	stage           string
	domainName      string
	proxySystemName string
}

type pubsubContext struct {
	subscriptionName string
	projectID        string
	subscriptionID   string
	messageID        string
}

var (
	supportedProxies = map[string]proxyDetails{
		"aws-apigateway": {
			spanName:  "aws.apigateway",
			component: "aws-apigateway",
		},
	}
)

func extractInferredProxyContext(headers http.Header) (*proxyContext, error) {
	_, exists := headers[ProxyHeaderStartTimeMs]
	if !exists {
		return nil, errors.New("proxy header start time does not exist")
	}

	proxyHeaderSystem, exists := headers[ProxyHeaderSystem]
	if !exists {
		return nil, errors.New("proxy header system does not exist")
	}

	if _, ok := supportedProxies[proxyHeaderSystem[0]]; !ok {
		return nil, errors.New("unsupported Proxy header system")
	}

	pc := proxyContext{
		method:          headers.Get(ProxyHeaderHTTPMethod),
		path:            headers.Get(ProxyHeaderPath),
		stage:           headers.Get(ProxyHeaderStage),
		domainName:      headers.Get(ProxyHeaderDomain),
		proxySystemName: headers.Get(ProxyHeaderSystem),
	}

	startTimeUnixMilli, err := strconv.ParseInt(headers[ProxyHeaderStartTimeMs][0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("error parsing time string: %s", err.Error())
	}
	pc.startTime = time.UnixMilli(startTimeUnixMilli)

	return &pc, nil
}

func startInferredProxySpan(requestProxyContext *proxyContext, parent *tracer.SpanContext, opts ...tracer.StartSpanOption) *tracer.Span {
	proxySpanInfo := supportedProxies[requestProxyContext.proxySystemName]
	log.Debug(`Successfully extracted inferred span info ${proxyContext} for proxy: ${proxyContext.proxySystemName}`)

	startTime := requestProxyContext.startTime

	configService := requestProxyContext.domainName
	if configService == "" {
		configService = globalconfig.ServiceName()
	}

	optsLocal := make([]tracer.StartSpanOption, len(opts), len(opts)+1)
	copy(optsLocal, opts)

	optsLocal = append(optsLocal,
		func(cfg *tracer.StartSpanConfig) {
			if cfg.Tags == nil {
				cfg.Tags = make(map[string]any)
			}

			cfg.Parent = parent
			cfg.StartTime = startTime

			cfg.Tags[ext.SpanType] = ext.SpanTypeWeb
			cfg.Tags[ext.ServiceName] = configService
			cfg.Tags[ext.Component] = proxySpanInfo.component
			cfg.Tags[ext.HTTPMethod] = requestProxyContext.method
			cfg.Tags[ext.HTTPURL] = requestProxyContext.domainName + requestProxyContext.path
			cfg.Tags[ext.HTTPRoute] = requestProxyContext.path
			cfg.Tags[ext.ResourceName] = fmt.Sprintf("%s %s", requestProxyContext.method, requestProxyContext.path)
			cfg.Tags["_dd.inferred_span"] = 1
			cfg.Tags["stage"] = requestProxyContext.stage
		},
	)

	span := tracer.StartSpan(proxySpanInfo.spanName, optsLocal...)

	return span
}

func startInferredSpanFromHeaders(headers http.Header) *tracer.Span {
	var inferredStartSpanOpts []tracer.StartSpanOption
	spanParentCtx, _ := tracer.Extract(tracer.HTTPHeadersCarrier(headers))
	if spanParentCtx != nil && spanParentCtx.SpanLinks() != nil {
		inferredStartSpanOpts = append(inferredStartSpanOpts, tracer.WithSpanLinks(spanParentCtx.SpanLinks()))
	}

	requestProxyContext, err := extractInferredProxyContext(headers)
	if err == nil {
		return startInferredProxySpan(requestProxyContext, spanParentCtx, inferredStartSpanOpts...)
	} else {
		log.Debug("unable to start inferred proxy span: %s\n", err.Error())
	}

	pubsubCtx := extractInferredPubsubContext(headers)
	if pubsubCtx != nil {
		return startInferredPubsubPushSubscriptionSpan(pubsubCtx, spanParentCtx, inferredStartSpanOpts...)
	} else {
		log.Debug("Unable to create inferred pubsub span. Skipping")
	}

	return nil
}

// extractInferredPubsubContext reads Pub/Sub push metadata headers and returns a
// fully populated pubsubContext, or an error if headers are missing or invalid.
func extractInferredPubsubContext(headers http.Header) *pubsubContext {
	subscriptionName := headers.Get(PubsubHeaderSubscriptionName)
	if subscriptionName == "" {
		return nil
	}

	parts := strings.Split(subscriptionName, "/")

	// check that subscriptionName matches format projects/{project_id}/subscriptions/{subscription_id}.
	if len(parts) != 4 || // must have 4 segments
		parts[0] != "projects" || parts[2] != "subscriptions" || // must match 'projects' and 'subscriptions'
		parts[1] == "" || parts[3] == "" { // project and subscription IDs can't be empty
		return nil
	}

	projectID := parts[1]
	subscriptionID := parts[3]

	messageID := headers.Get(PubsubHeaderMessageID)
	if messageID == "" {
		return nil
	}
	return &pubsubContext{
		subscriptionName: subscriptionName,
		projectID:        projectID,
		subscriptionID:   subscriptionID,
		messageID:        messageID,
	}
}

// startInferredPubsubPushSubscriptionSpan starts an inferred pubsub.receive consumer span
// for HTTP handlers that process gcp Pub/Sub push deliveries. The span is tagged like
// library-instrumented subscribe/receive paths so push-based workloads show the same
// messaging layer in the trace as pull/subscribe flows.
//
// See: https://cloud.google.com/pubsub/docs/push
func startInferredPubsubPushSubscriptionSpan(pubsubContex *pubsubContext, parent *tracer.SpanContext, opts ...tracer.StartSpanOption) *tracer.Span {
	configService := globalconfig.ServiceName()
	spanName := "pubsub.receive"
	component := "net/http"
	optsLocal := make([]tracer.StartSpanOption, len(opts), len(opts)+1)
	copy(optsLocal, opts)

	optsLocal = append(optsLocal,
		func(cfg *tracer.StartSpanConfig) {
			if cfg.Tags == nil {
				cfg.Tags = make(map[string]any)
			}

			cfg.Parent = parent
			cfg.Tags[ext.SpanType] = ext.SpanTypeMessageConsumer
			cfg.Tags[ext.SpanName] = spanName
			cfg.Tags[ext.ServiceName] = configService
			cfg.Tags[ext.Component] = component
			cfg.Tags[ext.ResourceName] = pubsubContex.subscriptionName
			cfg.Tags[ext.SpanKind] = ext.SpanKindConsumer
			cfg.Tags[ext.MessagingDestinationName] = pubsubContex.subscriptionID
			cfg.Tags[ext.MessagingOperationName] = "receive"
			cfg.Tags[ext.MessagingMessageID] = pubsubContex.messageID
			cfg.Tags[ext.PubsubMessageID] = pubsubContex.messageID // duplicate to align with existing pubsub tags for pull subscriptions
			cfg.Tags[ext.GCPProjectID] = pubsubContex.projectID
			cfg.Tags[ext.MessagingSystem] = ext.MessagingSystemGCPPubsub
			cfg.Tags["_dd.inferred_span"] = 1
		},
	)

	span := tracer.StartSpan(spanName, optsLocal...)

	return span
}
