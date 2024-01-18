// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pubsub

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

// A Option is used to customize spans started by WrapReceiveHandler or Publish.
type Option func(cfg *config)

// A ReceiveOption has been deprecated in favor of Option.
type ReceiveOption = Option

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
