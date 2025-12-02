// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_getConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Config
	}{
		{
			name: "No env",
			env:  map[string]string{},
			want: Config{
				ListenAddr:      ":8080",
				HealthCheckAddr: ":8081",
			},
		},
		{
			name: "All env",
			env: map[string]string{
				"DD_REQUEST_MIRROR_LISTEN_ADDR":      ":8888",
				"DD_REQUEST_MIRROR_HEALTHCHECK_ADDR": ":8181",
			},
			want: Config{
				ListenAddr:      ":8888",
				HealthCheckAddr: ":8181",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := getConfig()
			require.EqualValues(t, tt.want, got)
		})
	}
}
