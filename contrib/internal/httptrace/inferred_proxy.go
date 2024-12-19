package httptrace

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

const (
	PROXY_HEADER_SYSTEM = "X-Dd-Proxy"
	//PROXY_HEADER_START_TIME_MS = "x-dd-proxy-request-time-ms"
	PROXY_HEADER_START_TIME_MS = "X-Dd-Proxy-Request-Time-Ms"
	PROXY_HEADER_PATH          = "X-Dd-Proxy-Path"
	PROXY_HEADER_HTTPMETHOD    = "X-Dd-Proxy-Httpmethod"
	PROXY_HEADER_DOMAIN        = "X-Dd-Proxy-Domain-Name"
	PROXY_HEADER_STAGE         = "X-Dd-Proxy-Stage"
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
	//proxyContent := make(map[string][]string)

	_, exists := headers[PROXY_HEADER_START_TIME_MS]
	if !exists {
		println("no proxy header start time")
		return nil
	}

	proxyHeaderSystem, exists := headers[PROXY_HEADER_SYSTEM]
	if !exists {
		println("no proxy header system")
		return nil
	}
	if _, ok := supportedProxies[proxyHeaderSystem[0]]; !ok {
		println("unsupported Proxy header system")
		return nil
	}

	// Q: is it possible to have multiple values for any of these http headers??
	return &ProxyContext{
		RequestTime:     headers[PROXY_HEADER_START_TIME_MS][0],
		Method:          headers[PROXY_HEADER_HTTPMETHOD][0],
		Path:            headers[PROXY_HEADER_PATH][0],
		Stage:           headers[PROXY_HEADER_STAGE][0],
		DomainName:      headers[PROXY_HEADER_DOMAIN][0],
		ProxySystemName: headers[PROXY_HEADER_SYSTEM][0],
	}

}

func tryCreateInferredProxySpan(headers http.Header, parent ddtrace.SpanContext) ddtrace.SpanContext {
	println("IN TRYCREATE")
	println("headers are:")
	for key, values := range headers {
		fmt.Printf("Key: %s\n", key)
		println(key)
		for _, value := range values {
			fmt.Printf("  Value: %s\n", value)
			println(value)
		}
	}
	if headers == nil {
		println("headers nil")
		return nil

	}
	if !internal.BoolEnv(inferredProxyServicesEnabled, false) {
		println("bool env false")
		return nil
	}

	requestProxyContext := extractInferredProxyContext(headers)
	if requestProxyContext == nil {
		println("requestProxyContext nil")
		return nil
	}

	proxySpanInfo := supportedProxies[requestProxyContext.ProxySystemName]
	fmt.Printf(`Successfully extracted inferred span info ${proxyContext} for proxy: ${proxyContext.proxySystemName}`)

	// Parse Time string to Time Type
	millis, err := strconv.ParseInt(requestProxyContext.RequestTime, 10, 64)
	if err != nil {
		fmt.Println("Error parsing time string:", err)
		return nil
	}

	// Convert milliseconds to seconds and nanoseconds
	seconds := millis / 1000
	nanoseconds := (millis % 1000) * int64(time.Millisecond)

	// Create time.Time from Unix timestamp
	parsedTime := time.Unix(seconds, nanoseconds)

	config := ddtrace.StartSpanConfig{
		Parent: parent,
		//StartTime: requestProxyContext.RequestTime,
		StartTime: parsedTime,
		Tags: map[string]interface{}{
			"service":           requestProxyContext.DomainName,
			"HTTP_METHOD":       requestProxyContext.Method,
			"PATH":              requestProxyContext.Path,
			"STAGE":             requestProxyContext.Stage,
			"DOMAIN_NAME":       requestProxyContext.DomainName,
			"PROXY_SYSTEM_NAME": requestProxyContext.ProxySystemName,
		},
	}

	span := tracer.StartSpan(proxySpanInfo.SpanName, tracer.StartTime(config.StartTime), tracer.ChildOf(config.Parent), tracer.Tag("service", config.Tags["service"]))
	defer span.Finish()
	for k, v := range config.Tags {
		span.SetTag(k, v)
	}

	return span.Context()
}
