// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// parseLenientBool reads an observeFullEvaluationData-shaped RawMessage from the UFC and
// interprets it as a consent boolean. Anything that isn't a well-typed `true` — absent,
// null, wrong-typed (`"true"`, `1`, `[]`, `{}`) — falls to false. The privacy-preserving
// default wins over any parse ambiguity, but a wrong-typed value MUST NOT reject the whole
// UFC: agentless swallows the parse error, so rejection would strand a fresh pod on the
// SDK default forever.
func parseLenientBool(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(trimmed, &b); err != nil {
		return false
	}
	return b
}

// targetingKeyHashPrefix marks a targeting key as a fingerprint. Part of the wire contract:
// bare digests are rejected downstream.
const targetingKeyHashPrefix = "sha256_"

// hashTargetingKey produces the cross-SDK fingerprint carried on the privacy-protected path.
//
// Unsalted SHA-256 over the raw UTF-8 bytes — no trimming, case folding, or normalization —
// so every SDK produces a byte-identical digest and hashed values join across languages. A
// per-org salt is not available through the UFC today; unsalted is an accepted trade-off for
// server-side subjects.
func hashTargetingKey(targetingKey string) string {
	if targetingKey == "" {
		// Hashing "" would invent a shared pseudo-subject and corrupt unique-subject counts.
		// An absent targeting_key is schema-valid (the degraded tier omits it too).
		return ""
	}
	sum := sha256.Sum256([]byte(targetingKey))
	return targetingKeyHashPrefix + hex.EncodeToString(sum[:])
}
