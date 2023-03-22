// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "fmt"

type DBSystem string

const (
	DBSystemElasticsearch DBSystem = "elasticsearch"
)

type dbOutboundOperationNameSchema struct {
	cfg    *config
	system DBSystem
}

func NewDBOutboundOperationNameSchema(system DBSystem, opts ...Option) *Schema {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	return New(&dbOutboundOperationNameSchema{cfg: cfg, system: system})
}

func (d *dbOutboundOperationNameSchema) V0() string {
	if f, ok := d.cfg.versionOverrides[SchemaV0]; ok {
		return f()
	}
	return d.V1()
}

func (d *dbOutboundOperationNameSchema) V1() string {
	return fmt.Sprintf("%s.query", d.system)
}
