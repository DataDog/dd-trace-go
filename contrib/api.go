// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package contrib

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type Integration struct {
	mu                 sync.RWMutex
	defaultServiceName string
}

func (i *Integration) DefaultServiceName() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.defaultServiceName
}

func (i *Integration) DefaultAnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

type Option interface {
	apply(*Integration)
}

type OptionFn func(*Integration)

func (fn OptionFn) apply(cfg *Integration) {
	fn(cfg)
}

func LoadIntegration(name string, opts ...Option) *Integration {
	telemetry.LoadIntegration(name)
	integration := &Integration{}
	for _, opt := range opts {
		opt.apply(integration)
	}
	return nil
}

func WithServiceName(fallback string) OptionFn {
	return func(i *Integration) {
		i.defaultServiceName = namingschema.ServiceName(fallback)
	}
}

func WithServiceNameOverrideV0(fallback, overrideV0 string) OptionFn {
	return func(i *Integration) {
		i.defaultServiceName = namingschema.ServiceNameOverrideV0(fallback, overrideV0)
	}
}
