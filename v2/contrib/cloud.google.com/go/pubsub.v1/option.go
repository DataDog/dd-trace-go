// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pubsub

import (
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

type config struct {
	serviceName     string
	publishSpanName string
	receiveSpanName string
	measured        bool
}

func defaultConfig() *config {
	return &config{
		serviceName: namingschema.NewDefaultServiceName(
			"",
			namingschema.WithOverrideV0(""),
		).GetName(),
		publishSpanName: namingschema.NewGCPPubsubOutboundOp().GetName(),
		receiveSpanName: namingschema.NewGCPPubsubInboundOp().GetName(),
		measured:        false,
	}
}

// Option describes options for the pubsub integration.
type Option interface {
	apply(*config)
}

// OptionFn is used to customize spans started by WrapReceiveHandler or Publish.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// WithServiceName sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithServiceName(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() OptionFn {
	return func(cfg *config) {
		cfg.measured = true
	}
}