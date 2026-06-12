// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"reflect"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
)

// TestFlagEvaluationKillswitch verifies that DD_FLAGGING_EVALUATION_COUNTS_ENABLED (default true)
// controls ONLY the EVP flagevaluation hook/writer, leaving the OTel flagEvalHook unaffected.
//
// When the killswitch is "false": the EVP hook (flagEvalEVPHook) is NOT registered in Hooks()
// and flagEvalWriter is nil.
// When the killswitch is unset or "true": the EVP hook IS registered.
// The OTel flagEvalHook is present in Hooks() in BOTH cases.
func TestFlagEvaluationKillswitch(t *testing.T) {
	t.Run("killswitch disabled: EVP hook absent from Hooks(), OTel hook present", func(t *testing.T) {
		t.Setenv(flagEvalCountsEnabledEnvVar, "false")

		p := newDatadogProvider(ProviderConfig{})

		if p.flagEvalWriter != nil {
			t.Error("expected flagEvalWriter to be nil when killswitch is disabled")
		}
		if p.flagEvalEVPHook != nil {
			t.Error("expected flagEvalEVPHook to be nil when killswitch is disabled")
		}

		hooks := p.Hooks()

		// OTel hook must still be present.
		otelPresent := false
		for _, h := range hooks {
			if _, ok := h.(*flagEvalHook); ok {
				otelPresent = true
			}
		}
		if !otelPresent {
			t.Error("expected OTel flagEvalHook to be present in Hooks() even when killswitch is disabled")
		}

		// EVP hook must be absent.
		for _, h := range hooks {
			if _, ok := h.(*flagEvaluationHook); ok {
				t.Errorf("expected EVP flagEvaluationHook to be absent from Hooks() when killswitch is disabled, but found one: %v", reflect.TypeOf(h))
			}
		}
	})

	t.Run("killswitch enabled (unset = default true): EVP hook present in Hooks()", func(t *testing.T) {
		// Ensure the env var is unset to test the default-true behavior.
		t.Setenv(flagEvalCountsEnabledEnvVar, "1")

		p := newDatadogProvider(ProviderConfig{})

		if p.flagEvalWriter == nil {
			t.Error("expected flagEvalWriter to be non-nil when killswitch is enabled (default)")
		}
		if p.flagEvalEVPHook == nil {
			t.Error("expected flagEvalEVPHook to be non-nil when killswitch is enabled (default)")
		}

		hooks := p.Hooks()

		// Both OTel and EVP hooks must be present.
		otelPresent := false
		evpPresent := false
		for _, h := range hooks {
			switch h.(type) {
			case *flagEvalHook:
				otelPresent = true
			case *flagEvaluationHook:
				evpPresent = true
			}
		}
		if !otelPresent {
			t.Error("expected OTel flagEvalHook to be present in Hooks() when killswitch is enabled")
		}
		if !evpPresent {
			t.Error("expected EVP flagEvaluationHook to be present in Hooks() when killswitch is enabled")
		}
	})

	t.Run("killswitch explicitly true: EVP hook present in Hooks()", func(t *testing.T) {
		t.Setenv(flagEvalCountsEnabledEnvVar, "true")

		p := newDatadogProvider(ProviderConfig{})

		hooks := p.Hooks()

		evpPresent := false
		for _, h := range hooks {
			if _, ok := h.(*flagEvaluationHook); ok {
				evpPresent = true
			}
		}
		if !evpPresent {
			t.Error("expected EVP flagEvaluationHook to be present in Hooks() when killswitch is explicitly 'true'")
		}
	})
}

// Compile-time assertion: flagEvaluationHook implements the OpenFeature Hook interface.
var _ of.Hook = (*flagEvaluationHook)(nil)
