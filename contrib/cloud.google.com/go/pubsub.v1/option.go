// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pubsub

import "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2/internal/tracing"

// Option describes options for the Pub/Sub integration.
type Option = tracing.Option

// OptionFn represents options applicable to WrapReceiveHandler or Publish.
type OptionFn = tracing.OptionFn

// WithService sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithService(serviceName string) Option {
	return tracing.WithService(serviceName)
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() Option {
	return tracing.WithMeasured()
}
