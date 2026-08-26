// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "testing"

func TestCreateAPIKeyFingerprintMatchesCanonicalVectors(t *testing.T) {
	for _, test := range []struct {
		name   string
		apiKey string
		want   string
	}{
		{name: "empty", apiKey: "", want: "rijn_RZwTDmWjELXeEmMEb0eIIegKayGGUPNsuJweEPhlXi5"},
		{name: "leading zero padding", apiKey: "padding-171", want: "rijn_053ybBRXypQt9AC6UIlqH1YCFYSV1rQl8HCDIcBZs3D"},
		{name: "unicode", apiKey: "!@#$%^𐍈한€हИ£", want: "rijn_eFLHeyLxwaiNs2hY16pjkjNjVSHWRgf2rlveKc8YA1K"},
		{name: "ascii", apiKey: "secret", want: "rijn_amLaG4Pd6h6t9VtJna81k744P1DYxGHzIJ6ECO3OOMj"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := createAPIKeyFingerprint(test.apiKey)
			if got != test.want {
				t.Fatalf("createAPIKeyFingerprint(%q) = %q, want %q", test.apiKey, got, test.want)
			}
			if len(got) != 48 {
				t.Fatalf("len(createAPIKeyFingerprint(%q)) = %d, want 48", test.apiKey, len(got))
			}
		})
	}
}
