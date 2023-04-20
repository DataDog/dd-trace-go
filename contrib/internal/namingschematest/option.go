// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschematest

import "gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

type config struct {
	wantServiceName map[namingschema.Version]ServiceNameAssertions
}

func newConfig() *config {
	return &config{
		wantServiceName: make(map[namingschema.Version]ServiceNameAssertions, 0),
	}
}

// Option is a type used to customize behavior of functions in this package.
type Option func(*config)

// WithServiceNameAssertions allows you to override the service name assertions for a specific naming schema version.
func WithServiceNameAssertions(v namingschema.Version, s ServiceNameAssertions) Option {
	return func(cfg *config) {
		cfg.wantServiceName[v] = s
	}
}
