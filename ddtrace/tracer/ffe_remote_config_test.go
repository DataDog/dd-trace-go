// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/require"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

// TestFFERemoteConfigGating is the Go-side mirror of the parametric suite's
// _assert_no_ffe_remote_config_activation: the tracer must only subscribe to
// the FFE_FLAGS RC product when the resolved delivery source is remote_config.
func TestFFERemoteConfigGating(t *testing.T) {
	for name, tt := range map[string]struct {
		env            map[string]string
		wantSubscribed bool
	}{
		"default resolves to agentless, no RC subscription": {
			wantSubscribed: false,
		},
		"explicit source=agentless, no RC subscription": {
			env:            map[string]string{"DD_FEATURE_FLAGS_CONFIGURATION_SOURCE": "agentless"},
			wantSubscribed: false,
		},
		"explicit source=remote_config subscribes": {
			env:            map[string]string{"DD_FEATURE_FLAGS_CONFIGURATION_SOURCE": "remote_config"},
			wantSubscribed: true,
		},
		"legacy key true grandfathers remote_config": {
			env:            map[string]string{"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED": "true"},
			wantSubscribed: true,
		},
		"kill switch disables regardless of legacy key": {
			env: map[string]string{
				"DD_FEATURE_FLAGS_ENABLED":                  "false",
				"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED": "true",
			},
			wantSubscribed: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			t.Cleanup(remoteconfig.Reset)
			t.Cleanup(remoteconfig.Stop)
			internalffe.ResetForTest()
			t.Cleanup(internalffe.ResetForTest)

			trc, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
			require.NoError(t, err)
			t.Cleanup(stop)

			err = trc.startRemoteConfig(remoteconfig.DefaultClientConfig())
			require.NoError(t, err)

			found, err := remoteconfig.HasProduct(internalffe.FFEProductName)
			require.NoError(t, err)
			require.Equal(t, tt.wantSubscribed, found)
		})
	}
}
