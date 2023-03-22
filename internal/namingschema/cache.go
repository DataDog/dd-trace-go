// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

type CacheSystem string

const (
	CacheSystemMemcached CacheSystem = "memcached"
)

type cacheOutboundOperationNameSchema struct {
	cfg    *config
	system CacheSystem
}

func NewCacheOutboundOperationNameSchema(system CacheSystem, opts ...Option) *Schema {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	return New(&cacheOutboundOperationNameSchema{cfg: cfg, system: system})
}

func (c *cacheOutboundOperationNameSchema) V0() string {
	if f, ok := c.cfg.versionOverrides[SchemaV0]; ok {
		return f()
	}
	return c.V1()
}

func (c *cacheOutboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.command", c.system)
}
