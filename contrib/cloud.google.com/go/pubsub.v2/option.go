// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub

import "github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsubtrace"

// Option describes options for the Pub/Sub integration.
type Option = pubsubtrace.Option

// OptionFn represents options applicable to WrapReceiveHandler or Publish.
type OptionFn = pubsubtrace.OptionFn

// WithService sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithService(serviceName string) Option {
	return pubsubtrace.WithService(serviceName)
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() Option {
	return pubsubtrace.WithMeasured()
}
