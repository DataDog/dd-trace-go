// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"math/rand"
	"net/netip"

	internal "github.com/DataDog/appsec-internal-go/appsec"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type Feature struct {
	APISec internal.APISecConfig
}

func (*Feature) String() string {
	return "HTTP Security"
}

func (*Feature) Stop() {}

func NewHTTPSecFeature(config *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !config.SupportedAddresses.AnyOf(addresses.ServerRequestMethodAddr,
		addresses.ServerRequestRawURIAddr,
		addresses.ServerRequestHeadersNoCookiesAddr,
		addresses.ServerRequestCookiesAddr,
		addresses.ServerRequestQueryAddr,
		addresses.ServerRequestPathParamsAddr,
		addresses.ServerRequestBodyAddr,
		addresses.ServerResponseStatusAddr,
		addresses.ServerResponseHeadersNoCookiesAddr,
		addresses.ClientIPAddr,
	) {
		// We extract headers even when the security features are not enabled...
		feature := &BasicFeature{}
		dyngo.On(rootOp, feature.OnRequest)
		dyngo.OnFinish(rootOp, feature.OnResponse)
		return feature, nil
	}

	feature := &Feature{
		APISec: config.APISec,
	}

	dyngo.On(rootOp, feature.OnRequest)
	dyngo.OnFinish(rootOp, feature.OnResponse)
	return feature, nil
}

func (feature *Feature) OnRequest(op *httpsec.HandlerOperation, args httpsec.HandlerOperationArgs) {
	headers, ip := extractRequestHeaders(op, args)

	op.Run(op,
		addresses.NewAddressesBuilder().
			WithMethod(args.Method).
			WithRawURI(args.RequestURI).
			WithHeadersNoCookies(headers).
			WithCookies(args.Cookies).
			WithQuery(args.QueryParams).
			WithPathParams(args.PathParams).
			WithClientIP(ip).
			Build(),
	)
}

func (feature *Feature) OnResponse(op *httpsec.HandlerOperation, resp httpsec.HandlerOperationRes) {
	headers := extractResponseHeaders(op, resp)

	builder := addresses.NewAddressesBuilder().
		WithResponseHeadersNoCookies(headers).
		WithResponseStatus(resp.StatusCode)

	if feature.canExtractSchemas() {
		builder = builder.ExtractSchema()
	}

	op.Run(op, builder.Build())
}

// canExtractSchemas checks that API Security is enabled and that sampling rate
// allows extracting schemas
func (feature *Feature) canExtractSchemas() bool {
	return feature.APISec.Enabled && feature.APISec.SampleRate >= rand.Float64()
}

type BasicFeature struct{}

func (*BasicFeature) String() string {
	return "HTTP Header Extraction"
}

func (*BasicFeature) Stop() {}

func (*BasicFeature) OnRequest(op *httpsec.HandlerOperation, args httpsec.HandlerOperationArgs) {
	_, _ = extractRequestHeaders(op, args)
}

func (*BasicFeature) OnResponse(op *httpsec.HandlerOperation, resp httpsec.HandlerOperationRes) {
	_ = extractResponseHeaders(op, resp)
}

func extractRequestHeaders(op *httpsec.HandlerOperation, args httpsec.HandlerOperationArgs) (map[string][]string, netip.Addr) {
	tags, ip := ClientIPTags(args.Headers, true, args.RemoteAddr)
	log.Debug("appsec: http client ip detection returned `%s` given the http headers `%v`", ip, args.Headers)

	op.SetStringTags(tags)
	headers := headersRemoveCookies(args.Headers)
	headers["host"] = []string{args.Host}

	setRequestHeadersTags(op, headers)

	return headers, ip
}

func extractResponseHeaders(op *httpsec.HandlerOperation, resp httpsec.HandlerOperationRes) map[string][]string {
	headers := headersRemoveCookies(resp.Headers)
	setResponseHeadersTags(op, headers)
	return headers
}
