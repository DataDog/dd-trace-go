// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	"github.com/stretchr/testify/require"
)

func TestASMFeaturesCallback(t *testing.T) {
	enabledPayload := []byte(`{"asm":{"enabled":true}}`)
	disabledPayload := []byte(`{"asm":{"enabled":false}}`)

	env, set := os.LookupEnv(enabledEnvVar)
	defer func() {
		if set {
			os.Setenv(enabledEnvVar, env)
		}
	}()
	os.Unsetenv(enabledEnvVar)

	for _, tc := range []struct {
		name   string
		update remoteconfig.ProductUpdate
		// Should appsec be started before beginning the test
		startBefore bool
		// Is appsec expected to be started at the end of the test
		startedAfter bool
	}{
		{
			// This case shouldn't happen due to how callbacks dispatch work, but better safe than sorry
			name: "empty-update",
		},
		{
			name:         "enabled",
			update:       remoteconfig.ProductUpdate{"some/path": enabledPayload},
			startedAfter: true,
		},
		{
			name:        "disabled",
			update:      remoteconfig.ProductUpdate{"some/path": disabledPayload},
			startBefore: true,
		},
		{
			name:   "several-configs-1",
			update: remoteconfig.ProductUpdate{"some/path/1": disabledPayload, "some/path/2": enabledPayload},
		},
		{
			name:         "several-configs-2",
			update:       remoteconfig.ProductUpdate{"some/path/1": disabledPayload, "some/path/2": enabledPayload},
			startBefore:  true,
			startedAfter: true,
		},
		{
			name:   "bad-config-1",
			update: remoteconfig.ProductUpdate{"some/path": []byte("ImABadPayload")},
		},
		{
			name:         "bad-config-2",
			update:       remoteconfig.ProductUpdate{"some/path": []byte("ImABadPayload")},
			startBefore:  true,
			startedAfter: true,
		},
	} {
		Start()
		defer Stop()
		require.NotNil(t, activeAppSec)
		t.Run(tc.name, func(t *testing.T) {
			if tc.startBefore {
				activeAppSec.start()
			}
			require.Equal(t, tc.startBefore, activeAppSec.started)
			asmFeaturesCallback(tc.update)
			require.Equal(t, tc.startedAfter, activeAppSec.started)
		})
	}

	t.Run("enabled-twice", func(t *testing.T) {
		Start()
		defer Stop()
		update := remoteconfig.ProductUpdate{"some/path": enabledPayload}
		require.False(t, activeAppSec.started)
		asmFeaturesCallback(update)
		require.True(t, activeAppSec.started)
		asmFeaturesCallback(update)
		require.True(t, activeAppSec.started)
	})
	t.Run("disabled-twice", func(t *testing.T) {
		Start()
		defer Stop()
		update := remoteconfig.ProductUpdate{"some/path": disabledPayload}
		require.False(t, activeAppSec.started)
		asmFeaturesCallback(update)
		require.False(t, activeAppSec.started)
		asmFeaturesCallback(update)
		require.False(t, activeAppSec.started)
	})
}
