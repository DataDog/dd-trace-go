// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"errors"
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"net/http"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

	// ProxyHeaderHttpMethod is the header used to indicate the HTTP method
	// of the request (e.g., GET, POST, PUT, DELETE). This value corresponds
	// to 'context.httpMethod' in AWS API Gateway, and provides the method
	// used to make the request.
	ProxyHeaderHttpMethod = "X-Dd-Proxy-Httpmethod"

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
		method:          headers.Get(ProxyHeaderHttpMethod),
		path:            headers.Get(ProxyHeaderPath),
		stage:           headers.Get(ProxyHeaderStage),
		domainName:      headers.Get(ProxyHeaderDomain),
		proxySystemName: headers.Get(ProxyHeaderSystem),
	}

	startTimeUnixMilli, err := strconv.ParseInt(headers[ProxyHeaderStartTimeMs][0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("error parsing time string: %v", err)
	}
	pc.startTime = time.UnixMilli(startTimeUnixMilli)

	return &pc, nil
}

func startInferredProxySpan(requestProxyContext *proxyContext, parent ddtrace.SpanContext, opts ...ddtrace.StartSpanOption) tracer.Span {
	proxySpanInfo := supportedProxies[requestProxyContext.proxySystemName]
	log.Debug(`Successfully extracted inferred span info ${proxyContext} for proxy: ${proxyContext.proxySystemName}`)

	startTime := requestProxyContext.startTime

	configService := requestProxyContext.domainName
	if configService == "" {
		configService = globalconfig.ServiceName()
	}

	optsLocal := make([]ddtrace.StartSpanOption, len(opts), len(opts)+1)
	copy(optsLocal, opts)

	optsLocal = append(optsLocal,
		func(cfg *ddtrace.StartSpanConfig) {
			if cfg.Tags == nil {
				cfg.Tags = make(map[string]interface{})
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
