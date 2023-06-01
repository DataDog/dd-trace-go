// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema_test

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
)

func TestNewServiceNameSchema(t *testing.T) {
	defaultServiceName := "default"
	optOverrideV0 := namingschema.WithVersionOverride(namingschema.SchemaV0, "override-v0")
	optOverrideV1 := namingschema.WithVersionOverride(namingschema.SchemaV1, "override-v1")

	testCases := []struct {
		name                string
		schemaVersion       namingschema.Version
		serviceNameOverride string
		ddService           string
		beforeTestHook      func(t *testing.T) func()
		opts                []namingschema.Option
		want                string
	}{
		{
			name:                "schema v0",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "",
			opts:                nil,
			want:                "default",
		},
		{
			name:                "schema v0 with DD_SERVICE",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "dd-service",
			opts:                nil,
			want:                "dd-service",
		},
		{
			name:                "schema v0 with service override",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                nil,
			want:                "service-override",
		},
		{
			name:                "schema v0 version override",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                []namingschema.Option{optOverrideV0},
			want:                "override-v0",
		},
		{
			name:                "schema v1",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "",
			ddService:           "",
			opts:                nil,
			want:                "default",
		},
		{
			name:                "schema v1 with DD_SERVICE",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "",
			ddService:           "dd-service",
			opts:                nil,
			want:                "dd-service",
		},
		{
			name:                "schema v1 with service override",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                nil,
			want:                "service-override",
		},
		{
			name:                "schema v1 logic override",
			schemaVersion:       namingschema.SchemaV1,
			serviceNameOverride: "service-override",
			ddService:           "dd-service",
			opts:                []namingschema.Option{optOverrideV1},
			want:                "override-v1",
		},
		{
			name:                "schema v0 with defaults disabled",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "dd-service",
			beforeTestHook: func(_ *testing.T) func() {
				prev := namingschema.GetDefaultServiceNamesEnabled()
				namingschema.SetDefaultServiceNamesEnabled(false)
				return func() {
					namingschema.SetDefaultServiceNamesEnabled(prev)
				}
			},
			opts: nil,
			want: "dd-service",
		},
		{
			name:                "schema v0 with defaults enabled",
			schemaVersion:       namingschema.SchemaV0,
			serviceNameOverride: "",
			ddService:           "",
			beforeTestHook: func(_ *testing.T) func() {
				prev := namingschema.GetDefaultServiceNamesEnabled()
				namingschema.SetDefaultServiceNamesEnabled(true)
				return func() {
					namingschema.SetDefaultServiceNamesEnabled(prev)
				}
			},
			opts: nil,
			want: "default",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(tc.schemaVersion)

			if tc.beforeTestHook != nil {
				tc.beforeTestHook(t)
			}
			if tc.ddService != "" {
				svc := globalconfig.ServiceName()
				defer globalconfig.SetServiceName(svc)
				globalconfig.SetServiceName(tc.ddService)
			}
			s := namingschema.NewServiceNameSchema(tc.serviceNameOverride, defaultServiceName, tc.opts...)
			assert.Equal(t, tc.want, s.GetName())
		})
	}
}
