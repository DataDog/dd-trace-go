// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
)

// tracerPlugin allows ddtrace/tracer to start & stop AppSec features without
// creating a direct (static) dependency from the tracer package to this
// package. This is necessary to allow AppSec event listeners to use the tracer
// package to interact with trace spans.
type tracerPlugin struct{}

func (tracerPlugin) Start(rccfg remoteconfig.ClientConfig) {
	Start(config.WithRCConfig(rccfg))
}

func (tracerPlugin) Stop() {
	Stop()
}

func init() {
	tracer.RegisterPlugin(tracerPlugin{})
}
