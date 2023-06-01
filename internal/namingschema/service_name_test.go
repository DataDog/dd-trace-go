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

func TestNewDefaultServiceName(t *testing.T) {
	defaultServiceName := "default"
	optOverrideV0 := namingschema.WithOverrideV0("override-v0")

	testCases := []struct {
		name          string
		schemaVersion namingschema.Version
		ddService     string
		opts          []namingschema.Option
		want          string
	}{
		{
			name:          "schema v0",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "",
			opts:          nil,
			want:          "default",
		},
		{
			name:          "schema v0 with DD_SERVICE",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "dd-service",
			opts:          nil,
			want:          "dd-service",
		},
		{
			name:          "schema v0 override",
			schemaVersion: namingschema.SchemaV0,
			ddService:     "dd-service",
			opts:          []namingschema.Option{optOverrideV0},
			want:          "override-v0",
		},
		{
			name:          "schema v1",
			schemaVersion: namingschema.SchemaV1,
			ddService:     "",
			opts:          nil,
			want:          "default",
		},
		{
			name:          "schema v1 with DD_SERVICE",
			schemaVersion: namingschema.SchemaV1,
			ddService:     "dd-service",
			opts:          nil,
			want:          "dd-service",
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

			s := namingschema.NewDefaultServiceName(defaultServiceName, tc.opts...)
			assert.Equal(t, tc.want, s.GetName())
		})
	}
}
