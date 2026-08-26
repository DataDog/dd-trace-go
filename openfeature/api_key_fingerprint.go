// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"crypto/sha256"
	"math/big"
)

const (
	apiKeyFingerprintPrefix = "rijn_"
	sha256Base62Length      = 43
	base62Alphabet          = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

// createAPIKeyFingerprint returns the canonical non-secret identifier for an
// API key. SHA-256 is required for cross-SDK compatibility, not password
// storage or any other security decision.
func createAPIKeyFingerprint(apiKey string) string {
	digest := sha256.Sum256([]byte(apiKey))
	value := new(big.Int).SetBytes(digest[:])
	radix := big.NewInt(62)
	remainder := new(big.Int)
	encoded := make([]byte, 0, sha256Base62Length)
	for value.Sign() > 0 {
		value.QuoRem(value, radix, remainder)
		encoded = append(encoded, base62Alphabet[remainder.Int64()])
	}
	if len(encoded) == 0 {
		encoded = append(encoded, base62Alphabet[0])
	}
	for len(encoded) < sha256Base62Length {
		encoded = append(encoded, base62Alphabet[0])
	}
	for left, right := 0, len(encoded)-1; left < right; left, right = left+1, right-1 {
		encoded[left], encoded[right] = encoded[right], encoded[left]
	}
	return apiKeyFingerprintPrefix + string(encoded)
}
