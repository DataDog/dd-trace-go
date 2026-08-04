// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"os"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/ossec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
)

type ExecFeature struct{}

func (*ExecFeature) String() string {
	return "CMDi Protection"
}

func (*ExecFeature) Stop() {}

func NewExecSecFeature(cfg *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !cfg.RASP || !cfg.SupportedAddresses.AnyOf(addresses.ServerSysExecCmd) {
		return nil, nil
	}

	feature := &ExecFeature{}
	dyngo.On(rootOp, feature.OnStart)
	return feature, nil
}

func (*ExecFeature) OnStart(op *ossec.RunCommandOperation, args ossec.RunCommandOperationArgs) {
	dyngo.OnData(op, func(err *events.BlockingSecurityEvent) {
		dyngo.OnFinish(op, func(_ *ossec.RunCommandOperation, res ossec.RunCommandOperationRes[*os.Process]) {
			if res.Err != nil {
				*res.Err = err
			}
		})
	})

	ctxOp, ok := waf.ContextOperationFromParents(op)
	if !ok {
		return
	}

	subOp := ctxOp.NewSubcontextOp()
	defer subOp.Close()
	subOp.Run(op, addresses.NewAddressesBuilder().
		WithSysExecCmd(execCommandVector(args.Name, args.Commands)).
		Build())
}

// execCommandVector returns the argv to evaluate, forcing element 0 to the
// real executable (name) so the WAF sees the actual injected binary (RFC-0989),
// without mutating the caller's slice.
func execCommandVector(name string, argv []string) []string {
	if len(argv) == 0 {
		return []string{name}
	}
	out := append([]string(nil), argv...)
	out[0] = name
	return out
}
