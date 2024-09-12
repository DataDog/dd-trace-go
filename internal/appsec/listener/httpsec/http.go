// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"math/rand"

	"github.com/DataDog/appsec-internal-go/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
)

type Feature struct {
	APISec appsec.APISecConfig
}

var httpsecAddresses = []string{
	addresses.ServerRequestMethodAddr,
	addresses.ServerRequestRawURIAddr,
	addresses.ServerRequestHeadersNoCookiesAddr,
	addresses.ServerRequestCookiesAddr,
	addresses.ServerRequestQueryAddr,
	addresses.ServerRequestPathParamsAddr,
	addresses.ServerRequestBodyAddr,
	addresses.ServerResponseStatusAddr,
	addresses.ServerResponseHeadersNoCookiesAddr,
}

func NewHTTPSecFeature(config *config.Config, rootOp dyngo.Operation) (func(), error) {
	if !config.SupportedAddresses.AnyOf(httpsecAddresses...) {
		return func() {}, nil
	}

	feature := &Feature{
		APISec: config.APISec,
	}

	dyngo.On(rootOp, feature.OnRequest)
	dyngo.OnFinish(rootOp, feature.OnResponse)
	return func() {}, nil
}

func (feature *Feature) OnRequest(op *types.Operation, args types.HandlerOperationArgs) {
	op.Run(op,
		addresses.NewAddressesBuilder().
			WithMethod(args.Method).
			WithRawURI(args.RequestURI).
			WithHeadersNoCookies(args.Headers).
			WithCookies(args.Cookies).
			WithQuery(args.Query).
			WithPathParams(args.PathParams).
			WithClientIP(args.ClientIP).
			Build(),
	)
}

func (feature *Feature) OnResponse(op *types.Operation, args types.HandlerOperationRes) {
	builder := addresses.NewAddressesBuilder().
		WithResponseStatus(args.Status).
		WithHeadersNoCookies(args.Headers)

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
