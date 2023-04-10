// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import "gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

// NewServiceNameSchema returns a Schema with the standard logic to be used for contrib span service names
// (in-code override > DD_SERVICE environment variable > integration default name).
// If you need to support older versions not following this logic, you can use WithVersionOverride option to override this behavior.
func NewServiceNameSchema(userOverride, defaultName string, opts ...Option) *Schema {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	return New(&standardServiceNameSchema{
		userOverride: userOverride,
		defaultName:  defaultName,
		cfg:          cfg,
	})
}

type standardServiceNameSchema struct {
	userOverride string
	defaultName  string
	cfg          *config
}

func (s *standardServiceNameSchema) V0() string {
	return s.getName(SchemaV0)
}

func (s *standardServiceNameSchema) V1() string {
	return s.getName(SchemaV1)
}

func (s *standardServiceNameSchema) getName(v Version) string {
	if val, ok := s.cfg.versionOverrides[v]; ok {
		return val
	}
	if s.userOverride != "" {
		return s.userOverride
	}
	if svc := globalconfig.ServiceName(); svc != "" {
		return svc
	}
	return s.defaultName
}
