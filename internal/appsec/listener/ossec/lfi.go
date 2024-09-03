// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
)

type Feature struct{}

func NewOSSecFeature(cfg *config.Config, rootOp dyngo.Operation) (func(), error) {
	if !cfg.RASP || !cfg.SupportedAddresses.AnyOf(addresses.ServerIOFSFileAddr) {
		return func() {}, nil
	}

	feature := &Feature{}
	dyngo.On(rootOp, feature.OnStart)

	dyngo.OnData(rootOp, func(err *events.BlockingSecurityEvent) {
		dyngo.OnFinish(rootOp, func(op *ossec.OpenOperation, res ossec.OpenOperationRes[*os.File]) {
			if res.Err != nil {
				*res.Err = err
			}
		})
	})

	return func() {}, nil
}

func (*Feature) OnStart(op *ossec.OpenOperation, args ossec.OpenOperationArgs) {
	dyngo.EmitData(op,
		addresses.NewAddressesBuilder().
			WithFilePath(args.Path).
			Build(),
	)
}
