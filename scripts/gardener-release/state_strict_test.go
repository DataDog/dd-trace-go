// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func TestAuthoritativeStateJSONRejectsAmbiguousDocuments(t *testing.T) {
	var envelope SignedEnvelope
	for name, raw := range map[string]string{
		"duplicate":  `{"data":"eA==","signature_hex":"00","signature_hex":"11","public_key_hex":"00"}`,
		"unknown":    `{"data":"eA==","signature_hex":"00","public_key_hex":"00","extra":true}`,
		"null":       `null`,
		"trailing":   `{"data":"eA==","signature_hex":"00","public_key_hex":"00"} {}`,
		"wrong type": `{"data":1,"signature_hex":"00","public_key_hex":"00"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := decodeStrictStateJSON([]byte(raw), &envelope); err == nil {
				t.Fatal("ambiguous state JSON accepted")
			}
		})
	}
}
