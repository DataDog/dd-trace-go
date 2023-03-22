// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

func TestNewServiceNameSchema(t *testing.T) {
	defaultServiceName := "default"

	testCases := []struct {
		name                string
		schemaVersion       namingschema.Version
		serviceNameOverride string
		ddService           string
		opts                []namingschema.Option
		expected            string
	}{
		{
			name:                "schema v0",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "",
			opts:                nil,
			expected:            "default",
		},
		{
			name:                "schema v0 with DD_SERVICE",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "dd-service",
			opts:                nil,
			expected:            "dd-service",
		},
		{
			name:                "schema v0 with service override",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                nil,
			expected:            "service-override",
		},
		{
			name:                "schema v0 logic override",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts: []namingschema.Option{
				namingschema.WithVersionOverride(namingschema.SchemaV0, func() string {
					return "func-override"
				}),
			},
			expected: "func-override",
		},
		{
			name:                "schema v1",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "",
			ddService:           "",
			opts:                nil,
			expected:            "default",
		},
		{
			name:                "schema v1 with DD_SERVICE",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "",
			ddService:           "dd-service",
			opts:                nil,
			expected:            "dd-service",
		},
		{
			name:                "schema v1 with service override",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                nil,
			expected:            "service-override",
		},
		{
			name:                "schema v1 logic override",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts: []namingschema.Option{
				namingschema.WithVersionOverride(namingschema.SchemaV1, func() string {
					return "func-override"
				}),
			},
			expected: "func-override",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(tc.schemaVersion)

			if tc.ddService != "" {
				svc := globalconfig.ServiceName()
				defer globalconfig.SetServiceName(svc)
				globalconfig.SetServiceName(tc.ddService)
			}

			s := namingschema.NewServiceNameSchema(tc.serviceNameOverride, defaultServiceName, tc.opts...)
			assert.Equal(t, tc.expected, s.GetName())
		})
	}
}
