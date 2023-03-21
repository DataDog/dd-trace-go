// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package vault

import "gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

type serviceNameSchema struct {
	userOverride string
}

func newServiceNameSchema(userOverride string) namingschema.Schema {
	return namingschema.New(&serviceNameSchema{userOverride: userOverride})
}

func (s *serviceNameSchema) V0() string {
	if s.userOverride != "" {
		return s.userOverride
	}
	return "vault"
}

func (s *serviceNameSchema) V1() string {
	return s.V0()
}

type outboundOperationNameSchema struct{}

func newOutboundOperationNameSchema() namingschema.Schema {
	return namingschema.New(&outboundOperationNameSchema{})
}

func (o *outboundOperationNameSchema) V0() string {
	return "http.request"
}

func (o *outboundOperationNameSchema) V1() string {
	return "vault.query"
}
