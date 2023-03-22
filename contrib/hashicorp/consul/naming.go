// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package consul

import "gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

type serviceNameSchema struct{}

func newServiceNameSchema() *namingschema.Schema {
	return namingschema.New(&serviceNameSchema{})
}

func (s *serviceNameSchema) V0() string {
	return "consul"
}

func (s *serviceNameSchema) V1() string {
	return s.V0()
}

func newOutboundOperationNameSchema() *namingschema.Schema {
	return namingschema.NewDBOutboundOperationNameSchema(
		namingschema.DBSystemConsul,
		namingschema.WithVersionOverride(namingschema.SchemaV0, func() string {
			return "consul.command"
		}),
	)
}
