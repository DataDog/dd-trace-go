// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
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

type StartSpanOption = ddtrace.StartSpanOption

const (
	ProxyHeaderSystem      = "X-Dd-Proxy"
	ProxyHeaderStartTimeMs = "X-Dd-Proxy-Request-Time-Ms"
	ProxyHeaderPath        = "X-Dd-Proxy-Path"
	ProxyHeaderHttpMethod  = "X-Dd-Proxy-Httpmethod"
	ProxyHeaderDomain      = "X-Dd-Proxy-Domain-Name"
	ProxyHeaderStage       = "X-Dd-Proxy-Stage"
)

type ProxyDetails struct {
	SpanName  string `json:"spanName"`
	Component string `json:"component"`
}

var (
	supportedProxies = map[string]ProxyDetails{
		"aws-apigateway": {
			SpanName:  "aws.apigateway",
			Component: "aws-apigateway",
		},
	}
)

type ProxyContext struct {
	RequestTime     string `json:"requestTime"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Stage           string `json:"stage"`
	DomainName      string `json:"domainName"`
	ProxySystemName string `json:"proxySystemName"`
}

func extractInferredProxyContext(headers http.Header) *ProxyContext {
	_, exists := headers[ProxyHeaderStartTimeMs]
	if !exists {
		log.Debug("Proxy header start time does not exist")
		return nil
	}

	proxyHeaderSystem, exists := headers[ProxyHeaderSystem]
	if !exists {
		log.Debug("Proxy header system does not exist")
		return nil
	}

	if _, ok := supportedProxies[proxyHeaderSystem[0]]; !ok {
		log.Debug("Unsupported Proxy header system")
		return nil
	}

	return &ProxyContext{
		RequestTime:     headers[ProxyHeaderStartTimeMs][0],
		Method:          headers[ProxyHeaderHttpMethod][0],
		Path:            headers[ProxyHeaderPath][0],
		Stage:           headers[ProxyHeaderStage][0],
		DomainName:      headers[ProxyHeaderDomain][0],
		ProxySystemName: headers[ProxyHeaderSystem][0],
	}

}

func tryCreateInferredProxySpan(headers http.Header, parent ddtrace.SpanContext, opts ...StartSpanOption) tracer.Span {
	if headers == nil {
		log.Debug("Headers do not exist")
		return nil

	}

	requestProxyContext := extractInferredProxyContext(headers)
	if requestProxyContext == nil {
		log.Debug("Unable to extract inferred proxy context")
		return nil
	}

	proxySpanInfo := supportedProxies[requestProxyContext.ProxySystemName]
	log.Debug(`Successfully extracted inferred span info ${proxyContext} for proxy: ${proxyContext.proxySystemName}`)

	startTimeUnixMilli, err := strconv.ParseInt(requestProxyContext.RequestTime, 10, 64)
	if err != nil {
		log.Debug("Error parsing time string: %v", err)
		return nil
	}
	startTime := time.UnixMilli(startTimeUnixMilli)

	configService := requestProxyContext.DomainName
	if configService == "" {
		configService = globalconfig.ServiceName()
	}

	optsLocal := make([]StartSpanOption, len(opts), len(opts)+1)
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
			cfg.Tags[ext.Component] = proxySpanInfo.Component
			cfg.Tags[ext.HTTPMethod] = requestProxyContext.Method
			cfg.Tags[ext.HTTPURL] = requestProxyContext.DomainName + requestProxyContext.Path
			cfg.Tags[ext.HTTPRoute] = requestProxyContext.Path
			cfg.Tags[ext.ResourceName] = fmt.Sprintf("%s %s", requestProxyContext.Method, requestProxyContext.Path)
			cfg.Tags["stage"] = requestProxyContext.Stage
		},
	)

	span := tracer.StartSpan(proxySpanInfo.SpanName, optsLocal...)

	return span
}
