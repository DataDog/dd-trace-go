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

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
)

func TestASMFeaturesCallback(t *testing.T) {
	if waf.Health() != nil {
		t.Skip("WAF cannot be used")
	}
	enabledPayload := []byte(`{"asm":{"enabled":true}}`)
	disabledPayload := []byte(`{"asm":{"enabled":false}}`)
	cfg, err := newConfig()
	require.NoError(t, err)
	a := newAppSec(cfg)

	t.Setenv(enabledEnvVar, "")
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
		t.Run(tc.name, func(t *testing.T) {
			defer a.stop()
			require.NotNil(t, a)
			if tc.startBefore {
				a.start()
			}
			require.Equal(t, tc.startBefore, a.started)
			a.asmFeaturesCallback(tc.update)
			require.Equal(t, tc.startedAfter, a.started)
		})
	}

	t.Run("enabled-twice", func(t *testing.T) {
		defer a.stop()
		update := remoteconfig.ProductUpdate{"some/path": enabledPayload}
		require.False(t, a.started)
		a.asmFeaturesCallback(update)
		require.True(t, a.started)
		a.asmFeaturesCallback(update)
		require.True(t, a.started)
	})
	t.Run("disabled-twice", func(t *testing.T) {
		defer a.stop()
		update := remoteconfig.ProductUpdate{"some/path": disabledPayload}
		require.False(t, a.started)
		a.asmFeaturesCallback(update)
		require.False(t, a.started)
		a.asmFeaturesCallback(update)
		require.False(t, a.started)
	})
}

// We use the below placeholders to replace a real WAF handle for testing
type chanUpdater struct {
	resChan chan []rc.ASMDataRuleData
}

func rulesDataToMap(rulesData []rc.ASMDataRuleData) map[string]int64 {
	res := make(map[string]int64)
	for _, data := range rulesData {
		for _, v := range data.Data {
			res[data.ID+v.Value] = v.Expiration
		}
	}

	return res
}

func (u *chanUpdater) UpdateRulesData(rulesData []rc.ASMDataRuleData) error {
	u.resChan <- rulesData
	return nil
}

func TestASMDataCallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		update   remoteconfig.ProductUpdate
		expected []rc.ASMDataRuleData
		statuses map[string]rc.ApplyStatus
	}{
		{
			name:     "empty-rule-data",
			update:   map[string][]byte{},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateAcknowledged}},
		},
		{
			name: "bad-json",
			update: map[string][]byte{
				"some/path": []byte(`[}]`),
			},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateError}},
		},
		{
			name: "single-value",
			update: map[string][]byte{
				"some/path": []byte(`{"rules_data":[{"id":"test","type":"data_with_expiration","data":[{"expiration":3494138481,"value":"user1"}]}]}`),
			},
			expected: []rc.ASMDataRuleData{{ID: "test", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
				{Expiration: 3494138481, Value: "user1"},
			}}},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateAcknowledged}},
		},
		{
			name: "multiple-values",
			update: map[string][]byte{
				"some/path": []byte(`{"rules_data":[{"id":"test","type":"data_with_expiration","data":[{"expiration":3494138481,"value":"user1"},{"expiration":3494138441,"value":"user2"}]}]}`),
			},
			expected: []rc.ASMDataRuleData{{ID: "test", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
				{Expiration: 3494138481, Value: "user1"},
				{Expiration: 3494138441, Value: "user2"},
			}}},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateAcknowledged}},
		},
		{
			name: "multiple-entries",
			update: map[string][]byte{
				"some/path": []byte(`{"rules_data":[{"id":"test1","type":"data_with_expiration","data":[{"expiration":3494138444,"value":"user3"}]},{"id":"test2","type":"data_with_expiration","data":[{"expiration":3495138481,"value":"user4"}]}]}`),
			},
			expected: []rc.ASMDataRuleData{
				{ID: "test1", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
					{Expiration: 3494138444, Value: "user3"},
				}}, {ID: "test2", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
					{Expiration: 3495138481, Value: "user4"},
				}},
			},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateAcknowledged}},
		},
		{
			name: "merging-entries",
			update: map[string][]byte{
				"some/path/1": []byte(`{"rules_data":[{"id":"test1","type":"data_with_expiration","data":[{"expiration":3494138444,"value":"user3"}]},{"id":"test2","type":"data_with_expiration","data":[{"expiration":3495138481,"value":"user4"}]}]}`),
				"some/path/2": []byte(`{"rules_data":[{"id":"test1","type":"data_with_expiration","data":[{"expiration":3494138445,"value":"user3"}]},{"id":"test2","type":"data_with_expiration","data":[{"expiration":0,"value":"user5"}]}]}`),
			},
			expected: []rc.ASMDataRuleData{
				{ID: "test1", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
					{Expiration: 3494138445, Value: "user3"},
				}},
				{ID: "test2", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
					{Expiration: 3495138481, Value: "user4"},
					{Expiration: 0, Value: "user5"},
				}},
			},
			statuses: map[string]rc.ApplyStatus{
				"some/path/1": {State: rc.ApplyStateAcknowledged},
				"some/path/2": {State: rc.ApplyStateAcknowledged},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := chanUpdater{resChan: make(chan []rc.ASMDataRuleData, 4096)}
			handle := wafHandleWrapper{&u}
			defer close(u.resChan)
			statuses := handle.asmDataCallback(tc.update)
			// Check results by rule data ID since ordering is not guaranteed
			if tc.expected != nil {
				res := rulesDataToMap(<-u.resChan)
				exp := rulesDataToMap(tc.expected)
				require.Equal(t, exp, res)
			}
			for k := range statuses {
				require.Equal(t, tc.statuses[k].State, statuses[k].State)
				if statuses[k].State == rc.ApplyStateError {
					require.NotEmpty(t, statuses[k].Error)
				} else {
					require.Empty(t, statuses[k].Error)
				}
			}
		})
	}
}

// This test makes sure that the merging behavior follows what is described in the ASM blocking RFC
func TestRuleDataMerging(t *testing.T) {
	for _, tc := range []struct {
		name string
		in1  []rc.ASMDataRuleDataEntry
		in2  []rc.ASMDataRuleDataEntry
		out  []rc.ASMDataRuleDataEntry
	}{
		{
			name: "empty",
			out:  []rc.ASMDataRuleDataEntry{},
		},
		{
			name: "no-collision-1",
			in1: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
			},
			out: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
			},
		},
		{
			name: "no-collision-2",
			in1: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
			},
			in2: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.8",
					Expiration: 1,
				},
			},
			out: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
				{
					Value:      "127.0.0.8",
					Expiration: 1,
				},
			},
		},
		{
			name: "collision",
			in1: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
			},
			in2: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 2,
				},
			},
			out: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 2,
				},
			},
		},
		{
			name: "collision-no-expiration",
			in1: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 1,
				},
			},
			in2: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 0,
				},
			},
			out: []rc.ASMDataRuleDataEntry{
				{
					Value:      "127.0.0.1",
					Expiration: 0,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := mergeRulesDataEntries(tc.in1, tc.in2)
			resMap := rulesDataToMap([]rc.ASMDataRuleData{{ID: "test", Data: res}})
			expMap := rulesDataToMap([]rc.ASMDataRuleData{{ID: "test", Data: tc.out}})
			require.Equal(t, expMap, resMap)
		})
	}

}

// This test ensures that the remote activation capabilities are only set if DD_APPSEC_ENABLED is not set in the env.
func TestRemoteActivationScenarios(t *testing.T) {
	if waf.Health() != nil {
		t.Skip("WAF cannot be used")
	}

	t.Run("DD_APPSEC_ENABLED unset", func(t *testing.T) {
		t.Setenv(enabledEnvVar, "")
		os.Unsetenv(enabledEnvVar)
		Start(WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()

		require.NotNil(t, activeAppSec)
		require.False(t, Enabled())
		client := activeAppSec.rc
		require.NotNil(t, client)
		require.Contains(t, client.Capabilities, remoteconfig.ASMActivation)
		require.Contains(t, client.Products, rc.ProductASMFeatures)
	})

	t.Run("DD_APPSEC_ENABLED=true", func(t *testing.T) {
		t.Setenv(enabledEnvVar, "true")
		Start(WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()

		require.True(t, Enabled())
		client := activeAppSec.rc
		require.NotNil(t, client)
		require.NotContains(t, client.Capabilities, remoteconfig.ASMActivation)
		require.NotContains(t, client.Products, rc.ProductASMFeatures)
	})

	t.Run("DD_APPSEC_ENABLED=false", func(t *testing.T) {
		t.Setenv(enabledEnvVar, "false")
		Start(WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()
		require.Nil(t, activeAppSec)
		require.False(t, Enabled())
	})
}
