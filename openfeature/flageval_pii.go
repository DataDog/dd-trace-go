// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"crypto/sha256"
	"encoding/hex"
)

// targetingKeyHashPrefix marks a targeting key as a fingerprint rather than a subject
// identifier. It is part of the wire contract, not decoration: prefix + 64 hex characters is
// exactly 71 characters, and a bare digest is rejected downstream.
const targetingKeyHashPrefix = "sha256_"

// hashTargetingKey reduces a raw targeting key to the one-way fingerprint the flagevaluation
// track carries by default, preserving unique-subject counts per (flag, allocation) without
// transmitting the subject itself.
//
// The digest is unsalted SHA-256 over the raw UTF-8 bytes exactly as received — no trimming,
// no case folding, no Unicode normalization. That is deliberate rather than lazy: every SDK
// must produce a byte-identical digest for a given subject so hashed values join across
// languages and against the backend. Salting or normalizing here would silently break that
// join. Unsalted is an accepted trade-off (reversing a digest is judged low-risk for server
// applications); a per-org salt is not available through the UFC today.
//
// Called once per aggregation bucket at flush cadence, never per evaluation.
func hashTargetingKey(targetingKey string) string {
	if targetingKey == "" {
		// There is no subject to fingerprint. Hashing the empty string would invent one
		// constant pseudo-subject shared by every subject-less evaluation, which reads as a
		// real subject and corrupts unique-subject counts. An absent targeting_key is
		// schema-valid — the degraded tier omits it too.
		return ""
	}
	sum := sha256.Sum256([]byte(targetingKey))
	return targetingKeyHashPrefix + hex.EncodeToString(sum[:])
}
