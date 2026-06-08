// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike

import (
	"github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type clientConfig struct {
	serviceName   string
	serviceSource string
	operationName string
}

// ClientOption describes options for the Aerospike integration.
type ClientOption interface {
	apply(*clientConfig)
}

// ClientOptionFn represents options applicable to WrapClient.
type ClientOptionFn func(*clientConfig)

func (fn ClientOptionFn) apply(cfg *clientConfig) {
	fn(cfg)
}

func defaults(cfg *clientConfig) {
	cfg.serviceName = tracing.Instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.serviceSource = string(instrumentation.PackageAerospikeClientGoV7)
	cfg.operationName = tracing.Instr.OperationName(instrumentation.ComponentDefault, nil)
}

// WithService sets the given service name for the connection.
func WithService(name string) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}
