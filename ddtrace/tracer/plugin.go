// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import "gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

// Plugin allows features that depend on the tracer to be started and stopped together with it.
type Plugin interface {
	// Start is called whenever the tracer is started, and receives a RC client configuration object.
	Start(remoteconfig.ClientConfig)
	// Stop is called whenever the tracer is stopped.
	Stop()
}

// plugins stores all plugins registered at init-time, to be started & stopped together with the
// tracer. This allows features that depend on the tracer to be started & stopped by the tracer
// without causing a syntaxtic dependency from the tracer to these features; which in turns allows
// the features' packages to use functionality provided by the tracer package.
var plugins []Plugin

// RegisterPlugin registers a plugin to be started and stopped together with the tracer. This
// function should only be called from an `init` function, as it is unsafe to call concurrently.
func RegisterPlugin(plugin Plugin) {
	plugins = append(plugins, plugin)
}
