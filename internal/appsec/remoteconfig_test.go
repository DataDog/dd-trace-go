// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package appsec

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"

	internal "github.com/DataDog/appsec-internal-go/appsec"
	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	waf "github.com/DataDog/go-libddwaf/v2"
	"github.com/stretchr/testify/require"
)

func TestASMFeaturesCallback(t *testing.T) {
	if supported, _ := waf.Health(); !supported {
		t.Skip("WAF cannot be used")
	}
	enabledPayload := []byte(`{"asm":{"enabled":true}}`)
	disabledPayload := []byte(`{"asm":{"enabled":false}}`)
	cfg, err := config.NewConfig()
	require.NoError(t, err)
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
				a.start(nil)
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

func TestMergeRulesData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		update   remoteconfig.ProductUpdate
		expected []config.RuleDataEntry
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
			expected: []config.RuleDataEntry{{ID: "test", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
				{Expiration: 3494138481, Value: "user1"},
			}}},
			statuses: map[string]rc.ApplyStatus{"some/path": {State: rc.ApplyStateAcknowledged}},
		},
		{
			name: "multiple-values",
			update: map[string][]byte{
				"some/path": []byte(`{"rules_data":[{"id":"test","type":"data_with_expiration","data":[{"expiration":3494138481,"value":"user1"},{"expiration":3494138441,"value":"user2"}]}]}`),
			},
			expected: []config.RuleDataEntry{{ID: "test", Type: "data_with_expiration", Data: []rc.ASMDataRuleDataEntry{
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
			expected: []config.RuleDataEntry{
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
			expected: []config.RuleDataEntry{
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
			merged, statuses := mergeRulesData(tc.update)
			// Sort the compared elements since ordering is not guaranteed and the slice hold types that embed
			// more slices
			require.Len(t, merged, len(tc.expected))
			sort.Slice(merged, func(i, j int) bool {
				return strings.Compare(merged[i].ID, merged[j].ID) < 0
			})
			sort.Slice(tc.expected, func(i, j int) bool {
				return strings.Compare(merged[i].ID, merged[j].ID) < 0
			})

			for i := range tc.expected {
				require.Equal(t, tc.expected[i].ID, merged[i].ID)
				require.Equal(t, tc.expected[i].Type, merged[i].Type)
				require.ElementsMatch(t, tc.expected[i].Data, merged[i].Data)
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

// This test makes sure that the merging behavior for rule data entries follows what is described in the ASM blocking RFC
func TestMergeRulesDataEntries(t *testing.T) {
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
			require.ElementsMatch(t, tc.out, res)
		})
	}

}

// This test ensures that the remote activation capabilities are only set if DD_APPSEC_ENABLED is not set in the env.
func TestRemoteActivationScenarios(t *testing.T) {
	if supported, _ := waf.Health(); !supported {
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
		found, err = remoteconfig.HasProduct(rc.ProductASMFeatures)
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
		found, err = remoteconfig.HasProduct(rc.ProductASMFeatures)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("DD_APPSEC_ENABLED=false", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "false")
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()
		require.Nil(t, activeAppSec)
		require.False(t, Enabled())
	})
}

func TestCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		expected []remoteconfig.Capability
	}{
		{
			name:     "appsec-unspecified",
			expected: []remoteconfig.Capability{remoteconfig.ASMActivation},
		},
		{
			name:     "appsec-enabled/default-RulesManager",
			env:      map[string]string{config.EnvEnabled: "1"},
			expected: blockingCapabilities[:],
		},
		{
			name:     "appsec-enabled/RulesManager-from-env",
			env:      map[string]string{config.EnvEnabled: "1", internal.EnvRules: "testdata/blocking.json"},
			expected: []remoteconfig.Capability{},
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
			for _, cap := range tc.expected {
				found, err := remoteconfig.HasCapability(cap)
				require.NoError(t, err)
				require.True(t, found)
			}
		})
	}
}

func craftRCUpdates(fragments map[string]config.RulesFragment) map[string]remoteconfig.ProductUpdate {
	update := make(map[string]remoteconfig.ProductUpdate)
	for path, frag := range fragments {
		data, err := json.Marshal(frag)
		if err != nil {
			panic(err)
		}
		if len(frag.Rules) > 0 {
			if _, ok := update[rc.ProductASMDD]; !ok {
				update[rc.ProductASMDD] = make(remoteconfig.ProductUpdate)
			}
			update[rc.ProductASMDD][path] = data
		} else if len(frag.Overrides) > 0 || len(frag.Exclusions) > 0 || len(frag.Actions) > 0 {
			if _, ok := update[rc.ProductASM]; !ok {
				update[rc.ProductASM] = make(remoteconfig.ProductUpdate)
			}
			update[rc.ProductASM][path] = data
		} else if len(frag.RulesData) > 0 {
			if _, ok := update[rc.ProductASMData]; !ok {
				update[rc.ProductASMData] = make(remoteconfig.ProductUpdate)
			}
			update[rc.ProductASMData][path] = data
		}
	}

	return update
}

type testRulesOverrideEntry struct {
	ID          string        `json:"id,omitempty"`
	RulesTarget []interface{} `json:"rules_target,omitempty"`
	Enabled     interface{}   `json:"enabled,omitempty"`
	OnMatch     interface{}   `json:"on_match,omitempty"`
}

func TestOnRCUpdate(t *testing.T) {

	BaseRuleset, err := config.NewRulesManeger(nil)
	require.NoError(t, err)
	BaseRuleset.Compile()

	rules := config.RulesFragment{
		Version: BaseRuleset.Latest.Version,
		Rules: []interface{}{
			BaseRuleset.Base.Rules[0],
		},
	}

	overrides1 := config.RulesFragment{
		Overrides: []interface{}{
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
	overrides2 := config.RulesFragment{
		Overrides: []interface{}{
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
		name    string
		ruleset *config.RulesManager
	}{
		{
			name:    "no-updates",
			ruleset: BaseRuleset,
		},
		{
			name: "ASM/overrides/1-config",
			ruleset: &config.RulesManager{
				Base:     BaseRuleset.Base,
				BasePath: BaseRuleset.BasePath,
				Edits: map[string]config.RulesFragment{
					"overrides1/path": overrides1,
				},
			},
		},
		{
			name: "ASM/overrides/2-configs",
			ruleset: &config.RulesManager{
				Base:     BaseRuleset.Base,
				BasePath: BaseRuleset.BasePath,
				Edits: map[string]config.RulesFragment{
					"overrides1/path": overrides1,
					"overrides2/path": overrides2,
				},
			},
		},
		{
			name: "ASM_DD/1-config",
			ruleset: &config.RulesManager{
				Base:     rules,
				BasePath: "rules/path",
				Edits: map[string]config.RulesFragment{
					"rules/path": rules,
				},
			},
		},
		{
			name: "ASM_DD/2-configs (invalid)",
			ruleset: &config.RulesManager{
				Base:     BaseRuleset.Base,
				BasePath: BaseRuleset.BasePath,
				Edits: map[string]config.RulesFragment{
					"rules/path1": rules,
					"rules/path2": rules,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
			defer Stop()
			if !Enabled() {
				t.Skip()
			}

			tc.ruleset.Compile()
			// Craft and process the RC updates
			updates := craftRCUpdates(tc.ruleset.Edits)
			activeAppSec.onRCRulesUpdate(updates)
			// Compare rulesets
			require.Equal(t, activeAppSec.cfg.RulesManager.Raw(), activeAppSec.cfg.RulesManager.Raw())
		})
	}

	t.Run("post-stop", func(t *testing.T) {
		if supported, _ := waf.Health(); !supported {
			t.Skip("WAF needs to be available for this test (remote activation requirement)")
		}

		t.Setenv(config.EnvEnabled, "")
		os.Unsetenv(config.EnvEnabled)
		Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
		defer Stop()
		require.False(t, Enabled())

		enabledPayload := []byte(`{"asm":{"enabled":true}}`)
		// Activate appsec
		updates := map[string]remoteconfig.ProductUpdate{rc.ProductASMFeatures: map[string][]byte{"features/config": enabledPayload}}
		activeAppSec.onRemoteActivation(updates)
		require.True(t, Enabled())

		// Deactivate and try to update the rules. The rules update should not happen
		updates = map[string]remoteconfig.ProductUpdate{
			rc.ProductASMFeatures: map[string][]byte{"features/config": nil},
			rc.ProductASM:         map[string][]byte{"irrelevant/config": []byte("random payload that shouldn't even get unmarshalled")},
		}
		activeAppSec.onRemoteActivation(updates)
		require.False(t, Enabled())
		// Make sure rules did not get updated (callback gets short circuited when activeAppsec.started == false)
		RulesManager := activeAppSec.cfg.RulesManager
		statuses := activeAppSec.onRCRulesUpdate(updates)
		require.Empty(t, statuses)
		require.True(t, reflect.DeepEqual(RulesManager, activeAppSec.cfg.RulesManager))

	})
}

func TestOnRCUpdateStatuses(t *testing.T) {
	invalidRuleset, err := config.NewRulesManeger([]byte(`{"version": "2.2", "metadata": {"rules_version": "1.4.2"}, "rules": [{"id": "id","name":"name","tags":{},"conditions":[],"transformers":[],"on_match":[]}]}`))
	require.NoError(t, err)
	invalidRules := invalidRuleset.Base
	overrides := config.RulesFragment{
		Overrides: []interface{}{
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
	overrides2 := config.RulesFragment{
		Overrides: []interface{}{
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
	invalidOverrides := config.RulesFragment{
		Overrides: []interface{}{1, 2, 3, 4, "random data"},
	}
	ackStatus := genApplyStatus(true, nil)

	for _, tc := range []struct {
		name        string
		updates     map[string]remoteconfig.ProductUpdate
		expected    map[string]rc.ApplyStatus
		updateError bool
	}{
		{
			name:     "single/ack",
			updates:  craftRCUpdates(map[string]config.RulesFragment{"overrides": overrides}),
			expected: map[string]rc.ApplyStatus{"overrides": ackStatus},
		},
		{
			name:     "single/error",
			updates:  craftRCUpdates(map[string]config.RulesFragment{"invalid": invalidOverrides}),
			expected: map[string]rc.ApplyStatus{"invalid": ackStatus}, // Success, as there exists at least 1 usable rule in the whole set
		},
		{
			name:     "multiple/ack",
			updates:  craftRCUpdates(map[string]config.RulesFragment{"overrides": overrides, "overrides2": overrides2}),
			expected: map[string]rc.ApplyStatus{"overrides": ackStatus, "overrides2": ackStatus},
		},
		{
			name:    "multiple/single-error",
			updates: craftRCUpdates(map[string]config.RulesFragment{"overrides": overrides, "invalid": invalidOverrides}),
			expected: map[string]rc.ApplyStatus{
				"overrides": ackStatus, // Success, as there exists at least 1 usable rule in the whole set
				"invalid":   ackStatus, // Success, as there exists at least 1 usable rule in the whole set
			},
		},
		{
			name:        "multiple/all-errors",
			updates:     craftRCUpdates(map[string]config.RulesFragment{"overrides": overrides, "invalid": invalidRules}),
			updateError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Start(config.WithRCConfig(remoteconfig.DefaultClientConfig()))
			defer Stop()

			if !Enabled() {
				t.Skip("AppSec needs to be enabled for this test")
			}

			statuses := activeAppSec.onRCRulesUpdate(tc.updates)
			if tc.updateError {
				for _, status := range statuses {
					require.NotEmpty(t, status.Error)
					require.Equal(t, rc.ApplyStateError, status.State)
				}
			} else {
				require.Len(t, statuses, len(tc.expected))
				require.True(t, reflect.DeepEqual(tc.expected, statuses), "expected: %#v\nactual:   %#v", tc.expected, statuses)
			}
		})
	}
}

// TestWafUpdate tests that the WAF behaves correctly after the WAF handle gets updated with a new set of security rules
// through remote configuration
func TestWafRCUpdate(t *testing.T) {
	override := config.RulesFragment{
		// Override the already existing and enabled rule crs-913-120 with the "block" action
		Overrides: []interface{}{
			testRulesOverrideEntry{
				ID:      "crs-913-120",
				OnMatch: []string{"block"},
			},
		},
	}

	if supported, _ := waf.Health(); !supported {
		t.Skip("WAF needs to be available for this test")
	}

	t.Run("toggle-blocking", func(t *testing.T) {
		cfg, err := config.NewConfig()
		require.NoError(t, err)
		wafHandle, err := waf.NewHandle(cfg.RulesManager.Latest, cfg.Obfuscator.KeyRegex, cfg.Obfuscator.ValueRegex)
		require.NoError(t, err)
		defer wafHandle.Close()
		wafCtx := waf.NewContext(wafHandle)
		defer wafCtx.Close()
		values := map[string]interface{}{
			httpsec.ServerRequestPathParamsAddr: "/rfiinc.txt",
		}
		// Make sure the rule matches as expected
		result := sharedsec.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, cfg.WAFTimeout)
		require.Contains(t, jsonString(t, result.Events), "crs-913-120")
		require.Empty(t, result.Actions)
		// Simulate an RC update that disables the rule
		statuses, err := combineRCRulesUpdates(cfg.RulesManager, craftRCUpdates(map[string]config.RulesFragment{"override": override}))
		require.NoError(t, err)
		for _, status := range statuses {
			require.Equal(t, status.State, rc.ApplyStateAcknowledged)
		}
		cfg.RulesManager.Compile()
		newWafHandle, err := waf.NewHandle(cfg.RulesManager.Latest, cfg.Obfuscator.KeyRegex, cfg.Obfuscator.ValueRegex)
		require.NoError(t, err)
		defer newWafHandle.Close()
		newWafCtx := waf.NewContext(newWafHandle)
		defer newWafCtx.Close()
		// Make sure the rule returns a blocking action when matching
		result = sharedsec.RunWAF(newWafCtx, waf.RunAddressData{Persistent: values}, cfg.WAFTimeout)
		require.Contains(t, jsonString(t, result.Events), "crs-913-120")
		require.Contains(t, result.Actions, "block")
	})
}

func jsonString(t *testing.T, v any) string {
	bytes, err := json.Marshal(v)
	require.NoError(t, err)
	return string(bytes)
}
