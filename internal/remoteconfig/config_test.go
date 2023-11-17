// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package remoteconfig

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_pollIntervalFromEnv(t *testing.T) {
	defaultInterval := time.Second * time.Duration(5.0)
	tests := []struct {
		name  string
		setup func(t *testing.T)
		want  time.Duration
	}{
		{
			name:  "default",
			setup: func(t *testing.T) {},
			want:  defaultInterval,
		},
		{
			name:  "float",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "0.2") },
			want:  time.Millisecond * 200,
		},
		{
			name:  "integer",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "2") },
			want:  time.Second * 2,
		},
		{
			name:  "negative",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "-1") },
			want:  defaultInterval,
		},
		{
			name:  "zero",
			setup: func(t *testing.T) { t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "0") },
			want:  time.Nanosecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			assert.Equal(t, tt.want, pollIntervalFromEnv())
		})
	}
}
