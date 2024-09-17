// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"math/rand"

	"github.com/DataDog/appsec-internal-go/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type Feature struct {
	APISec appsec.APISecConfig
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
		addresses.ServerResponseHeadersNoCookiesAddr) {
		return nil, nil
	}

	feature := &Feature{
		APISec: config.APISec,
	}

	dyngo.On(rootOp, feature.OnRequest)
	dyngo.OnFinish(rootOp, feature.OnResponse)
	return feature, nil
}

func (feature *Feature) OnRequest(op *httpsec.HandlerOperation, args httpsec.HandlerOperationArgs) {
	tags, ip := ClientIPTags(args.Header, true, args.RemoteAddr)
	log.Debug("appsec: http client ip detection returned `%s` given the http headers `%v`", ip, args.Header)

	op.SetStringTags(tags)
	setRequestHeadersTags(op, args.Header)

	headers := headersRemoveCookies(args.Header)
	headers["host"] = []string{args.Host}

	op.Run(op,
		addresses.NewAddressesBuilder().
			WithMethod(args.Method).
			WithRawURI(args.RequestURI).
			WithHeadersNoCookies(headers).
			WithCookies(makeCookies(args.Cookies())).
			WithQuery(args.URL.Query()).
			WithPathParams(args.PathParams).
			WithClientIP(ip).
			Build(),
	)
}

func (feature *Feature) OnResponse(op *httpsec.HandlerOperation, args httpsec.HandlerOperationRes) {
	respHeaders := args.ResponseHeaderCopier(args.ResponseWriter)
	builder := addresses.NewAddressesBuilder().
		WithResponseHeadersNoCookies(respHeaders)

	setResponseHeadersTags(op, respHeaders)

	// Check if the underlying type of the response writer has a status method (e.g. like net/http.responseWriter)
	if mw, ok := args.ResponseWriter.(interface{ Status() int }); ok {
		builder = builder.WithResponseStatus(mw.Status())
	}

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
