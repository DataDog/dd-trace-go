// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pubsub

import "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2/internal/config"

// Deprecated: ReceiveOption has been deprecated in favor of Option.
type ReceiveOption = Option

// Option describes options for the Pub/Sub integration.
type Option = config.Option

// OptionFn represents options applicable to WrapReceiveHandler or Publish.
type OptionFn func(*config.Config)

func (fn OptionFn) Apply(cfg *config.Config) {
	fn(cfg)
}

var _ Option = OptionFn(nil)

// WithService sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithService(serviceName string) OptionFn {
	return func(cfg *config.Config) {
		cfg.ServiceName = serviceName
	}
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() OptionFn {
	return func(cfg *config.Config) {
		cfg.Measured = true
	}
}
