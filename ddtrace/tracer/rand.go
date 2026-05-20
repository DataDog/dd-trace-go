// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math"
	"math/rand/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// secureRandom is true when DD_TRACE_SECURE_RANDOM=true. Evaluated once at
// package init so hot paths avoid a repeated env-var lookup on every span.
var secureRandom = env.Get("DD_TRACE_SECURE_RANDOM") == "true"

// csrngUint64 draws 8 bytes from the kernel entropy pool (getrandom/urandom).
// The kernel entropy pool is re-seeded on process restore, so each resumed
// instance draws fresh entropy rather than replaying a captured PRNG state.
func csrngUint64() uint64 {
	var b [8]byte
	_, _ = cryptorand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

func randUint64() uint64 {
	if secureRandom {
		return csrngUint64()
	}
	return rand.Uint64()
}

func generateSpanID(_ int64) uint64 {
	if secureRandom {
		return csrngUint64() & math.MaxInt64
	}
	return rand.Uint64() & math.MaxInt64
}
