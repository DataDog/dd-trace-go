// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type config struct {
	serviceName     string
	publishSpanName string
	receiveSpanName string
	measured        bool
}

func defaultConfig() *config {
	return &config{
		serviceName:     namingschema.ServiceNameOverrideV0("", ""),
		publishSpanName: namingschema.OpName(namingschema.GCPPubSubOutbound),
		receiveSpanName: namingschema.OpName(namingschema.GCPPubSubInbound),
		measured:        false,
	}
}

// Option is used to customize spans started by WrapReceiveHandler or Publish.
type Option func(cfg *config)

// WithServiceName sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() Option {
	return func(cfg *config) {
		cfg.measured = true
	}
}
