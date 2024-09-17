// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pubsub

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2"
)

// A Option is used to customize spans started by WrapReceiveHandler or Publish.
type Option = v2.Option

// A ReceiveOption has been deprecated in favor of Option.
type ReceiveOption = Option

// WithServiceName sets the service name tag for traces started by WrapReceiveHandler or Publish.
func WithServiceName(serviceName string) Option {
	return v2.WithService(serviceName)
}

// WithMeasured sets the measured tag for traces started by WrapReceiveHandler or Publish.
func WithMeasured() Option {
	return v2.WithMeasured()
}
