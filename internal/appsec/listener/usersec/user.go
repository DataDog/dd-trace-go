// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package usersec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/usersec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
)

type Feature struct{}

func NewUserSecFeature(cfg *config.Config, rootOp dyngo.Operation) (func(), error) {
	if !cfg.SupportedAddresses.AnyOf(addresses.UserIDAddr) {
		return func() {}, nil
	}

	feature := &Feature{}
	dyngo.On(rootOp, feature.OnStart)
	return func() {}, nil
}

func (*Feature) OnStart(op *usersec.UserIDOperation, args usersec.UserIDOperationArgs) {
	dyngo.EmitData(op,
		addresses.NewAddressesBuilder().
			WithUserID(args.UserID).
			Build(),
	)
}
