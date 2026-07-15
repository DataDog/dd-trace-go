// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// This file is temporary data-gathering instrumentation for the "multiple Start
// calls" config problem (see the cross-product-Start design doc). It measures how
// often a product's Start call would observe a changed environment relative to the
// last recorded Start call, across all products, so we can scope customer blast
// radius before changing how the shared Config singleton reacts to repeat Start
// calls. Remove this file once that migration decision is made and the data is no
// longer needed.

package config

import (
	"hash/fnv"
	"sort"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	// startMu protects lastEnvHash and lastProduct, kept separate from mu since
	// Start-call history isn't tied to the Config singleton itself.
	startMu     sync.Mutex
	lastEnvHash uint64
	// lastProduct is "" until the first recorded Start call; no Product constant
	// is empty, so that doubles as the "no prior call recorded yet" sentinel.
	lastProduct Product
)

// RecordProductStart reports telemetry when the DD_*/OTEL_* environment has changed
// since the last recorded call, by any product. Call it once near the top of a
// product's Start function (tracer.Start, profiler.Start, etc.), regardless of
// whether that product's own configuration is backed by internal/config yet.
func RecordProductStart(product Product) {
	hash := envSnapshotHash()

	startMu.Lock()
	defer startMu.Unlock()
	if lastProduct != "" && hash != lastEnvHash {
		telemetry.Count(telemetry.NamespaceGeneral, "config.repeat_start_env_diff", []string{
			"trigger_product:" + string(product),
			"previous_product:" + string(lastProduct),
		}).Submit(1)
		log.Warn("config: environment variables changed since %s last called Start (this call: %s); "+
			"if unintentional, another library or dependency in this process may already have called Start",
			lastProduct, product)
	}
	lastEnvHash, lastProduct = hash, product
}

// envSnapshotHash covers the full supported-configuration surface rather than just
// the keys the calling product's Config reads, so it stays meaningful as more
// products migrate onto internal/config.
func envSnapshotHash() uint64 {
	keys := make([]string, 0, len(env.SupportedConfigurations))
	for k := range env.SupportedConfigurations {
		if env.IsSensitive(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := fnv.New64a()
	for _, k := range keys {
		if v, ok := env.Lookup(k); ok {
			h.Write([]byte(k))
			h.Write([]byte{'='})
			h.Write([]byte(v))
			h.Write([]byte{';'})
		}
	}
	return h.Sum64()
}
