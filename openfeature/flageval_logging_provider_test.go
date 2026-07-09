// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
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
	tests := []struct {
		name           string
		envValue       string
		wantEVPEnabled bool
	}{
		{
			name:           "killswitch disabled: EVP hook absent from Hooks(), OTel hook present",
			envValue:       "false",
			wantEVPEnabled: false,
		},
		{
			// "1" exercises the default-true behavior (any truthy value enables the EVP path).
			name:           "killswitch enabled (unset = default true): EVP hook present in Hooks()",
			envValue:       "1",
			wantEVPEnabled: true,
		},
		{
			name:           "killswitch explicitly true: EVP hook present in Hooks()",
			envValue:       "true",
			wantEVPEnabled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(flagEvalCountsEnabledEnvVar, tc.envValue)

			p := newDatadogProvider(ProviderConfig{})

			if tc.wantEVPEnabled {
				if p.flagEvalLoggingWriter == nil {
					t.Error("expected flagEvalWriter to be non-nil when killswitch is enabled")
				}
				if p.flagEvalLoggingHook == nil {
					t.Error("expected flagEvalEVPHook to be non-nil when killswitch is enabled")
				}
			} else {
				if p.flagEvalLoggingWriter != nil {
					t.Error("expected flagEvalWriter to be nil when killswitch is disabled")
				}
				if p.flagEvalLoggingHook != nil {
					t.Error("expected flagEvalEVPHook to be nil when killswitch is disabled")
				}
			}

			hooks := p.Hooks()

			otelPresent := false
			evpPresent := false
			for _, h := range hooks {
				switch h.(type) {
				case *flagEvalMetricsHook:
					otelPresent = true
				case *flagEvalLoggingHook:
					evpPresent = true
				}
			}

			// The OTel hook must be present in EVERY case — the killswitch never affects it.
			if !otelPresent {
				t.Error("expected OTel flagEvalHook to be present in Hooks() regardless of the killswitch")
			}

			if evpPresent != tc.wantEVPEnabled {
				if tc.wantEVPEnabled {
					t.Error("expected EVP flagEvalLoggingHook to be present in Hooks() when killswitch is enabled")
				} else {
					t.Errorf("expected EVP flagEvalLoggingHook to be absent from Hooks() when killswitch is disabled, but found one")
				}
			}

			if tc.wantEVPEnabled && p.exposureWriter.evp != p.flagEvalLoggingWriter.evp {
				t.Error("expected exposures and flag evaluations to share one EVP client")
			}
		})
	}
}

// Compile-time assertion: flagEvalLoggingHook implements the OpenFeature Hook interface.
var _ of.Hook = (*flagEvalLoggingHook)(nil)
