// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqlsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
)

type Feature struct{}

func NewSQLSecFeature(cfg *config.Config, rootOp dyngo.Operation) (func(), error) {
	if !cfg.RASP || !cfg.SupportedAddresses.AnyOf(addresses.ServerDBTypeAddr, addresses.ServerDBStatementAddr) {
		return func() {}, nil
	}

	feature := &Feature{}
	dyngo.On(rootOp, feature.OnStart)
	return func() {}, nil
}

func (*Feature) OnStart(op *sqlsec.SQLOperation, args sqlsec.SQLOperationArgs) {
	dyngo.EmitData(op, waf.RunEvent{
		Operation: op,
		RunAddressData: addresses.NewAddressesBuilder().
			WithDBStatement(args.Query).
			WithDBType(args.Driver).
			Build(),
	})
}
