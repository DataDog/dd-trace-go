// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
)

type SSRFProtectionFeature struct{}

func NewSSRFProtectionFeature(config *config.Config, rootOp dyngo.Operation) (func(), error) {
	if !config.RASP || !config.SupportedAddresses.AnyOf(addresses.ServerIoNetURLAddr) {
		return func() {}, nil
	}

	feature := &SSRFProtectionFeature{}
	dyngo.On(rootOp, feature.OnStart)
	return func() {}, nil
}

func (*SSRFProtectionFeature) OnStart(op *httpsec.RoundTripOperation, args httpsec.RoundTripOperationArgs) {
	dyngo.EmitData(op, addresses.NewAddressesBuilder().WithURL(args.URL).Build())
}
