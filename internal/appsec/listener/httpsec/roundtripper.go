// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec/types"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/sharedsec"

	"github.com/DataDog/appsec-internal-go/limiter"
	"github.com/DataDog/go-libddwaf/v3"
)

type SSRFProtectionFeature struct{}

func (*SSRFProtectionFeature) String() string {
	return "SSRF Protection"
}

func (*SSRFProtectionFeature) Stop() {}

func NewSSRFProtectionFeature(config *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !config.RASP || !config.SupportedAddresses.AnyOf(addresses.ServerIoNetURLAddr) {
		return nil, nil
	}

	feature := &SSRFProtectionFeature{}
	dyngo.On(rootOp, feature.OnStart)
	return feature, nil
}

func (*SSRFProtectionFeature) OnStart(op *httpsec.RoundTripOperation, args httpsec.RoundTripOperationArgs) {
	dyngo.EmitData(op, waf.RunEvent{
		Operation:      op,
		RunAddressData: addresses.NewAddressesBuilder().WithURL(args.URL).Build(),
	})
}
