// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package appsec

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	internal "github.com/DataDog/appsec-internal-go/appsec"
	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/timer"
	"github.com/stretchr/testify/require"
)

func TestASMFeaturesCallback(t *testing.T) {
	if supported, _ := libddwaf.Usable(); !supported {
		t.Skip("WAF cannot be used")
	}
	enabledPayload := []byte(`{"asm":{"enabled":true}}`)
	disabledPayload := []byte(`{"asm":{"enabled":false}}`)
	cfg, err := config.NewStartConfig().NewConfig()
	require.NoError(t, err)
	defer cfg.WAFManager.Close()

	a := newAppSec(cfg)
	err = a.startRC()
	require.NoError(t, err)

	t.Setenv(config.EnvEnabled, "")
	os.Unsetenv(config.EnvEnabled)

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
				require.NoError(t, a.start())
			}
			require.Equal(t, tc.startBefore, a.started)
			a.handleASMFeatures(tc.update)
			require.Equal(t, tc.startedAfter, a.started)
		})
	}

	t.Run("enabled-twice", func(t *testing.T) {
		defer a.stop()
		update := remoteconfig.ProductUpdate{"some/path": enabledPayload}
		require.False(t, a.started)
		a.handleASMFeatures(update)
		require.True(t, a.started)
		a.handleASMFeatures(update)
		require.True(t, a.started)
	})
	t.Run("disabled-twice", func(t *testing.T) {
		defer a.stop()
		update := remoteconfig.ProductUpdate{"some/path": disabledPayload}
		require.False(t, a.started)
		a.handleASMFeatures(update)
		require.False(t, a.started)
		a.handleASMFeatures(update)
		require.False(t, a.started)
	})
}

// This test ensures that the remote activation capabilities are only set if DD_APPSEC_ENABLED is not set in the env.
func TestRemoteActivationScenarios(t *testing.T) {
	if supported, _ := libddwaf.Usable(); !supported {
		t.Skip("WAF cannot be used")
	}

	t.Run("DD_APPSEC_ENABLED unset", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "")
		os.Unsetenv(config.EnvEnabled)
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()

		require.NotNil(t, activeAppSec)
		require.False(t, Enabled())
		found, err := remoteconfig.HasCapability(remoteconfig.ASMActivation)
		require.NoError(t, err)
		require.True(t, found)
		found, err = remoteconfig.HasProduct(state.ProductASMFeatures)
		require.NoError(t, err)
		require.True(t, found)
	})

	t.Run("DD_APPSEC_ENABLED=true", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "true")
		remoteconfig.Reset()
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()

		require.True(t, Enabled())
		found, err := remoteconfig.HasCapability(remoteconfig.ASMActivation)
		require.NoError(t, err)
		require.False(t, found)
		found, err = remoteconfig.HasProduct(state.ProductASMFeatures)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("WithEnablementMode(EnabledModeForcedOn)", func(t *testing.T) {
		for _, envVal := range []string{"", "true", "false"} {
			t.Run(fmt.Sprintf("DD_APPSEC_ENABLED=%s", envVal), func(t *testing.T) {
				t.Setenv(config.EnvEnabled, envVal)

				remoteconfig.Reset()
				Start(config.WithEnablementMode(config.ForcedOn), config.WithRCConfig(remoteconfig.DefaultClientConfig()))
				defer Stop()

				require.True(t, Enabled())
				found, err := remoteconfig.HasCapability(remoteconfig.ASMActivation)
				require.NoError(t, err)
				require.False(t, found)
				found, err = remoteconfig.HasProduct(state.ProductASMFeatures)
				require.NoError(t, err)
				require.False(t, found)
			})
		}
	})

	t.Run("DD_APPSEC_ENABLED=false", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "false")
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()
		require.Nil(t, activeAppSec)
		require.False(t, Enabled())
	})

	t.Run("WithEnablementMode(EnabledModeForcedOff)", func(t *testing.T) {
		for _, envVal := range []string{"", "true", "false"} {
			t.Run(fmt.Sprintf("DD_APPSEC_ENABLED=%s", envVal), func(t *testing.T) {
				t.Setenv(config.EnvEnabled, envVal)

				Start(config.WithEnablementMode(config.ForcedOff), config.WithRCConfig(remoteconfig.DefaultClientConfig()))
				defer Stop()
				require.Nil(t, activeAppSec)
				require.False(t, Enabled())
			})
		}
	})
}

func TestCapabilitiesAndProducts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		expectedC []remoteconfig.Capability
		expectedP []string
	}{
		{
			name:      "appsec-unspecified",
			expectedC: []remoteconfig.Capability{remoteconfig.ASMActivation},
			expectedP: []string{state.ProductASMFeatures},
		},
		{
			name: "appsec-enabled/default-RulesManager",
			env:  map[string]string{config.EnvEnabled: "1"},
			expectedC: func() []remoteconfig.Capability {
				result := make([]remoteconfig.Capability, 0, len(baseCapabilities)+len(blockingCapabilities))
				result = append(result, baseCapabilities[:]...)
				result = append(result, blockingCapabilities[:]...)
				return result
			}(),
			expectedP: []string{state.ProductASM, state.ProductASMData, state.ProductASMDD},
		},
		{
			name:      "appsec-enabled/RulesManager-from-env",
			env:       map[string]string{config.EnvEnabled: "1", internal.EnvRules: "testdata/blocking.json"},
			expectedC: []remoteconfig.Capability{},
			expectedP: []string{},
		},
	} {

		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvEnabled, "")
			os.Unsetenv(config.EnvEnabled)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
			defer Stop()
			if !Enabled() && activeAppSec == nil {
				t.Skip()
			}

			for _, cap := range tc.expectedC {
				found, err := remoteconfig.HasCapability(cap)
				require.NoError(t, err)
				require.True(t, found)
			}
			for _, p := range tc.expectedP {
				found, err := remoteconfig.HasProduct(p)
				require.NoError(t, err)
				require.True(t, found)
			}
		})
	}
}

func TestCapabilitiesAndProductsBlockingUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		expectedC []remoteconfig.Capability
		excludedC []remoteconfig.Capability
		expectedP []string
	}{
		{
			name:      "appsec-enabled/default-RulesManager",
			env:       map[string]string{config.EnvEnabled: "1"},
			expectedC: baseCapabilities[:],
			excludedC: blockingCapabilities[:],
			expectedP: []string{state.ProductASM, state.ProductASMData, state.ProductASMDD},
		},
	} {

		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvEnabled, "")
			os.Unsetenv(config.EnvEnabled)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()), config.WithBlockingUnavailable(true))
			defer Stop()
			if !Enabled() && activeAppSec == nil {
				t.Skip()
			}

			for _, cap := range tc.expectedC {
				found, err := remoteconfig.HasCapability(cap)
				require.NoError(t, err)
				require.True(t, found)
			}
			for _, cap := range tc.excludedC {
				found, err := remoteconfig.HasCapability(cap)
				require.NoError(t, err)
				require.False(t, found)
			}
			for _, p := range tc.expectedP {
				found, err := remoteconfig.HasProduct(p)
				require.NoError(t, err)
				require.True(t, found)
			}
		})
	}
}

func craftRCUpdates(fragments map[string]*RulesFragment) map[string]remoteconfig.ProductUpdate {
	update := make(map[string]remoteconfig.ProductUpdate)
	for path, frag := range fragments {
		if frag == nil {
			if _, ok := update[state.ProductASMDD]; !ok {
				update[state.ProductASMDD] = make(remoteconfig.ProductUpdate)
			}
			update[state.ProductASMDD][path] = nil
			continue
		}

		data, err := json.Marshal(frag)
		if err != nil {
			panic(err)
		}
		if len(frag.Rules) > 0 {
			if _, ok := update[state.ProductASMDD]; !ok {
				update[state.ProductASMDD] = make(remoteconfig.ProductUpdate)
			}
			update[state.ProductASMDD][path] = data
		} else if len(frag.Overrides) > 0 || len(frag.Exclusions) > 0 || len(frag.Actions) > 0 {
			if _, ok := update[state.ProductASM]; !ok {
				update[state.ProductASM] = make(remoteconfig.ProductUpdate)
			}
			update[state.ProductASM][path] = data
		} else if len(frag.RulesData) > 0 || len(frag.ExclusionData) > 0 {
			if _, ok := update[state.ProductASMData]; !ok {
				update[state.ProductASMData] = make(remoteconfig.ProductUpdate)
			}
			update[state.ProductASMData][path] = data
		}
	}

	return update
}

type testRulesOverrideEntry struct {
	ID          string `json:"id,omitempty"`
	RulesTarget []any  `json:"rules_target,omitempty"`
	Enabled     any    `json:"enabled,omitempty"`
	OnMatch     any    `json:"on_match,omitempty"`
}

func TestOnRCUpdate(t *testing.T) {
	var defaultRules RulesFragment
	require.NoError(t, json.Unmarshal([]byte(internal.StaticRecommendedRules), &defaultRules))

	rules := RulesFragment{
		Version:  defaultRules.Version,
		Metadata: defaultRules.Metadata,
		Rules: []any{
			defaultRules.Rules[0],
		},
	}

	// Test rules overrides
	t.Run("Overrides", func(t *testing.T) {
		overrides1 := RulesFragment{
			Overrides: []any{
				testRulesOverrideEntry{
					ID:      "crs-941-290",
					Enabled: false,
				},
				testRulesOverrideEntry{
					ID:      "crs-930-100",
					Enabled: false,
				},
			},
		}
		overrides2 := RulesFragment{
			Overrides: []any{
				testRulesOverrideEntry{
					ID:      "crs-941-300",
					Enabled: false,
				},
				testRulesOverrideEntry{
					Enabled: false,
					ID:      "crs-921-160",
				},
			},
		}

		for _, tc := range []struct {
			name     string
			edits    map[string]*RulesFragment
			statuses map[string]state.ApplyStatus
		}{
			{
				name:     "no-updates",
				statuses: map[string]state.ApplyStatus{},
			},
			{
				name: "ASM/overrides/1-config",
				edits: map[string]*RulesFragment{
					"overrides1/path": &overrides1,
				},
				statuses: map[string]state.ApplyStatus{
					"overrides1/path": {State: state.ApplyStateAcknowledged},
				},
			},
			{
				name: "ASM/overrides/2-configs",
				edits: map[string]*RulesFragment{
					"overrides1/path": &overrides1,
					"overrides2/path": &overrides2,
				},
				statuses: map[string]state.ApplyStatus{
					"overrides1/path": {State: state.ApplyStateAcknowledged},
					"overrides2/path": {State: state.ApplyStateAcknowledged},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
				defer Stop()
				if !Enabled() {
					t.Skip()
				}

				// Craft and process the RC updates
				updates := craftRCUpdates(tc.edits)
				statuses := activeAppSec.onRCRulesUpdate(updates)
				require.Equal(t, tc.statuses, statuses)

				// Make sure edits are added to the active ruleset
				expected := []string{"ASM_DD/default"}
				for path := range tc.statuses {
					expected = append(expected, path)
				}
				slices.Sort(expected)
				actual := activeAppSec.cfg.WAFManager.ConfigPaths()
				slices.Sort(actual)
				require.Equal(t, expected, actual)
			})
		}

	})

	// Test rules update (ASM_DD)
	for _, tc := range []struct {
		name                string
		initialBasePath     string
		expectedConfigPaths []string
		edits               map[string]*RulesFragment
		statuses            map[string]state.ApplyStatus
		removal             string
	}{
		{
			name:     "no-updates",
			statuses: map[string]state.ApplyStatus{},
		},
		{
			name:                "ASM_DD/1-config",
			expectedConfigPaths: []string{"rules/path"},
			edits: map[string]*RulesFragment{
				"rules/path": &rules,
			},
			statuses: map[string]state.ApplyStatus{
				"rules/path": {State: state.ApplyStateAcknowledged},
			},
		},
		{
			name:                "ASM_DD/2-configs",
			expectedConfigPaths: []string{"rules/path1"},
			edits: map[string]*RulesFragment{
				"rules/path1": &rules,
				"rules/path2": &rules,
			},
			statuses: map[string]state.ApplyStatus{
				"rules/path1": {State: state.ApplyStateAcknowledged},
				"rules/path2": {State: state.ApplyStateError, Error: `{"rules":{"errors":{"duplicate rule":["blk-001-001"]}}}`},
			},
		},
		{
			name:                "ASM_DD/1-config-1-removal",
			expectedConfigPaths: []string{"rules/path1"},
			edits: map[string]*RulesFragment{
				"rules/path1": &rules,
				"rules/v1":    nil,
			},
			statuses: map[string]state.ApplyStatus{
				"rules/path1": {State: state.ApplyStateAcknowledged},
				"rules/v1":    {State: state.ApplyStateAcknowledged},
			},
			removal: "rules/v1",
		},
		{
			name:            "ASM_DD/1-removal",
			initialBasePath: "rules/path",
			edits: map[string]*RulesFragment{
				"rules/path": &rules,
			},
			statuses: map[string]state.ApplyStatus{
				"rules/path": {State: state.ApplyStateAcknowledged},
			},
			removal: "rules/path",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
			defer Stop()
			if !Enabled() {
				t.Skip()
			}

			require.Equal(t, []string{"ASM_DD/default"}, activeAppSec.cfg.WAFManager.ConfigPaths())

			// Craft and process the RC updates
			updates := craftRCUpdates(tc.edits)
			if tc.removal != "" {
				updates[state.ProductASMDD][tc.removal] = nil
			}

			statuses := activeAppSec.onRCRulesUpdate(updates)
			require.Equal(t, tc.statuses, statuses)

			// Compare rulesets base paths to make sure the updates were processed correctly
			expected := tc.expectedConfigPaths
			if expected == nil {
				expected = []string{"ASM_DD/default"}
			}
			require.Equal(t, expected, activeAppSec.cfg.WAFManager.ConfigPaths())
		})
	}

	t.Run("post-stop", func(t *testing.T) {
		if supported, _ := libddwaf.Usable(); !supported {
			t.Skip("WAF needs to be available for this test (remote activation requirement)")
		}

		t.Setenv(config.EnvEnabled, "")
		os.Unsetenv(config.EnvEnabled)
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()
		require.False(t, Enabled())

		enabledPayload := []byte(`{"asm":{"enabled":true}}`)
		// Activate appsec
		updates := map[string]remoteconfig.ProductUpdate{state.ProductASMFeatures: map[string][]byte{"features/config": enabledPayload}}
		activeAppSec.onRemoteActivation(updates)
		require.True(t, Enabled())

		// Deactivate and try to update the rules. The rules update should not happen
		updates = map[string]remoteconfig.ProductUpdate{
			state.ProductASMFeatures: map[string][]byte{"features/config": nil},
			state.ProductASM:         map[string][]byte{"irrelevant/config": []byte("random payload that shouldn't even get unmarshalled")},
		}
		activeAppSec.onRemoteActivation(updates)
		require.False(t, Enabled())
		// Make sure rules did not get updated (callback gets short circuited when activeAppsec.started == false)
		statuses := activeAppSec.onRCRulesUpdate(updates)
		require.Equal(t,
			map[string]state.ApplyStatus{
				"features/config":   {State: state.ApplyStateUnacknowledged},
				"irrelevant/config": {State: state.ApplyStateUnacknowledged},
			},
			statuses,
		)
		require.NotContains(t, activeAppSec.cfg.WAFManager.ConfigPaths(), "irrelevant/config")
	})
}

func TestOnRCUpdateStatuses(t *testing.T) {
	var invalidRules RulesFragment
	require.NoError(t, json.Unmarshal([]byte(`{"version": "2.2", "metadata": {"rules_version": "1.4.2"}, "rules": [{"id": "id","name":"name","tags":{},"conditions":[],"transformers":[],"on_match":[]}]}`), &invalidRules))
	overrides := RulesFragment{
		Overrides: []any{
			testRulesOverrideEntry{
				ID:      "rule-1",
				Enabled: true,
			},
			testRulesOverrideEntry{
				ID:      "rule-2",
				Enabled: false,
			},
		},
	}
	overrides2 := RulesFragment{
		Overrides: []any{
			testRulesOverrideEntry{
				ID:      "rule-3",
				Enabled: true,
			},
			testRulesOverrideEntry{
				ID:      "rule-4",
				Enabled: false,
			},
		},
	}
	invalidOverrides := RulesFragment{
		Overrides: []any{1, 2, 3, 4, "random data"},
	}
	ackStatus := state.ApplyStatus{State: state.ApplyStateAcknowledged}

	for _, tc := range []struct {
		name     string
		updates  map[string]remoteconfig.ProductUpdate
		expected map[string]state.ApplyStatus
	}{
		{
			name:     "single/ack",
			updates:  craftRCUpdates(map[string]*RulesFragment{"overrides": &overrides}),
			expected: map[string]state.ApplyStatus{"overrides": ackStatus},
		},
		{
			name:     "single/error",
			updates:  craftRCUpdates(map[string]*RulesFragment{"invalid": &invalidOverrides}),
			expected: map[string]state.ApplyStatus{"invalid": {State: state.ApplyStateError, Error: `{"rules_overrides":{"error":"bad cast, expected 'map', obtained 'float'"}}`}},
		},
		{
			name:     "multiple/ack",
			updates:  craftRCUpdates(map[string]*RulesFragment{"overrides": &overrides, "overrides2": &overrides2}),
			expected: map[string]state.ApplyStatus{"overrides": ackStatus, "overrides2": ackStatus},
		},
		{
			name:    "multiple/single-error",
			updates: craftRCUpdates(map[string]*RulesFragment{"overrides": &overrides, "invalid": &invalidOverrides}),
			expected: map[string]state.ApplyStatus{
				"overrides": ackStatus,
				"invalid":   {State: state.ApplyStateError, Error: `{"rules_overrides":{"error":"bad cast, expected 'map', obtained 'float'"}}`},
			},
		},
		{
			name:    "multiple/all-errors",
			updates: craftRCUpdates(map[string]*RulesFragment{"overrides": &invalidOverrides, "invalid": &invalidRules}),
			expected: map[string]state.ApplyStatus{
				"overrides": {State: state.ApplyStateError, Error: `{"rules_overrides":{"error":"bad cast, expected 'map', obtained 'float'"}}`},
				"invalid":   {State: state.ApplyStateError, Error: `{"rules":{"errors":{"rule has no valid conditions":["id"]}}}`},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
			defer Stop()

			if !Enabled() {
				t.Skip("AppSec needs to be enabled for this test")
			}

			statuses := activeAppSec.onRCRulesUpdate(tc.updates)
			require.Equal(t, tc.expected, statuses)
		})
	}
}

// TestWafUpdate tests that the WAF behaves correctly after the WAF handle gets updated with a new set of security rules
// through remote configuration
func TestWafRCUpdate(t *testing.T) {
	override := RulesFragment{
		// Override the already existing and enabled rule crs-913-120 with the "block" action
		Overrides: []any{
			testRulesOverrideEntry{
				ID:      "crs-913-120",
				OnMatch: []string{"block"},
			},
		},
	}

	if supported, _ := libddwaf.Usable(); !supported {
		t.Skip("WAF needs to be available for this test")
	}

	t.Run("toggle-blocking", func(t *testing.T) {
		cfg, err := config.NewStartConfig().NewConfig()
		require.NoError(t, err)
		appsec := appsec{cfg: cfg, started: true}

		wafHandle, _ := appsec.cfg.NewHandle()
		require.NotNil(t, wafHandle)
		defer wafHandle.Close()
		wafCtx, err := wafHandle.NewContext(timer.WithBudget(time.Hour))
		require.NoError(t, err)
		defer wafCtx.Close()
		values := map[string]any{
			addresses.ServerRequestPathParamsAddr: "/rfiinc.txt",
		}

		// Make sure the rule matches as expected
		result, err := wafCtx.Run(libddwaf.RunAddressData{Persistent: values})
		require.NoError(t, err)
		require.Contains(t, jsonString(t, result.Events), "crs-913-120")
		require.Empty(t, result.Actions)

		// Simulate an RC update that disables the rule
		statuses := appsec.onRCRulesUpdate(craftRCUpdates(map[string]*RulesFragment{"override": &override}))
		require.Subset(t, statuses, map[string]state.ApplyStatus{"override": {State: state.ApplyStateAcknowledged}})
		wafHandle, _ = appsec.cfg.NewHandle()
		require.NotNil(t, wafHandle)
		defer wafHandle.Close()
		newWafCtx, err := wafHandle.NewContext(timer.WithBudget(time.Hour))
		require.NoError(t, err)
		defer newWafCtx.Close()
		// Make sure the rule returns a blocking action when matching
		result, err = newWafCtx.Run(libddwaf.RunAddressData{Persistent: values})
		require.NoError(t, err)
		require.Contains(t, jsonString(t, result.Events), "crs-913-120")
		require.Contains(t, result.Actions, "block_request")
	})
}

func jsonString(t *testing.T, v any) string {
	bytes, err := json.Marshal(v)
	require.NoError(t, err)
	return string(bytes)
}

// RulesFragment can represent a full ruleset or a fragment of it.
type RulesFragment struct {
	Version       string                  `json:"version,omitempty"`
	Metadata      any                     `json:"metadata,omitempty"`
	Rules         []any                   `json:"rules,omitempty"`
	Overrides     []any                   `json:"rules_override,omitempty"`
	Exclusions    []any                   `json:"exclusions,omitempty"`
	ExclusionData []state.ASMDataRuleData `json:"exclusion_data,omitempty"`
	RulesData     []state.ASMDataRuleData `json:"rules_data,omitempty"`
	Actions       []any                   `json:"actions,omitempty"`
	CustomRules   []any                   `json:"custom_rules,omitempty"`
	Processors    []any                   `json:"processors,omitempty"`
	Scanners      []any                   `json:"scanners,omitempty"`
}
