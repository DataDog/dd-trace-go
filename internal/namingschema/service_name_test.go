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

func TestServiceName(t *testing.T) {
	defaultServiceName := "default"

	testCases := []struct {
		name          string
		schemaVersion namingschema.Version
		ddService     string
		setup         func() func()
		call          func() string
		want          string
	}{
		{
			name:          "v0",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "",
			call:          func() string { return namingschema.ServiceName(defaultServiceName) },
			want:          "default",
		},
		{
			name:          "v0-DD_SERVICE",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "dd-service",
			call:          func() string { return namingschema.ServiceName(defaultServiceName) },
			want:          "dd-service",
		},
		{
			name:          "v0-override",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "dd-service",
			call:          func() string { return namingschema.ServiceNameOverrideV0(defaultServiceName, "override-v0") },
			want:          "override-v0",
		},
		{
			name:          "v1",
			schemaVersion: namingschema.SchemaV1,
			ddService:     "",
			call:          func() string { return namingschema.ServiceName(defaultServiceName) },
			want:          "default",
		},
		{
			name:          "v1-DD_SERVICE",
			schemaVersion: namingschema.SchemaV1,
			ddService:     "dd-service",
			call:          func() string { return namingschema.ServiceName(defaultServiceName) },
			want:          "dd-service",
		},
		{
			name:          "v0-UseGlobalServiceName",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "dd-service",
			setup: func() func() {
				prev := namingschema.UseGlobalServiceName()
				namingschema.SetUseGlobalServiceName(true)
				return func() {
					namingschema.SetUseGlobalServiceName(prev)
				}
			},
			call: func() string { return namingschema.ServiceNameOverrideV0(defaultServiceName, "override-v0") },
			want: "dd-service",
		},
		{
			name:          "v0-UseGlobalServiceName",
			schemaVersion: namingschema.SchemaV1,
			ddService:     "dd-service",
			setup: func() func() {
				prev := namingschema.UseGlobalServiceName()
				namingschema.SetUseGlobalServiceName(true)
				return func() {
					namingschema.SetUseGlobalServiceName(prev)
				}
			},
			call: func() string { return namingschema.ServiceNameOverrideV0(defaultServiceName, "override-v0") },
			want: "dd-service",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(tc.schemaVersion)

			if tc.setup != nil {
				cleanup := tc.setup()
				defer cleanup()
			}
			if tc.ddService != "" {
				svc := globalconfig.ServiceName()
				defer globalconfig.SetServiceName(svc)
				globalconfig.SetServiceName(tc.ddService)
			}
			s := tc.call()
			assert.Equal(t, tc.want, s)
		})
	}
}
