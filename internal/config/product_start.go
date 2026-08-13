// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Temporary data-gathering instrumentation for the cross-product-Start config
// problem; remove once the migration decision is made.

package config

import (
	"hash/fnv"
	"sort"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	// startMu is separate from mu because it protects Start-call bookkeeping,
	// not the Config singleton itself.
	startMu     sync.Mutex
	lastEnvHash uint64
	// lastProduct is "" until the first recorded Start call.
	lastProduct Product
)

// RecordProductStart reports telemetry when the env has changed since the last
// recorded call by any product. Call near the top of a product's Start function.
//
// Known limitation: it can't distinguish a customer-driven env change from
// dd-trace-go's own bootstrap mutations, so diffs are an upper bound on real
// cross-product blast radius.
func RecordProductStart(product Product) {
	startMu.Lock()
	defer startMu.Unlock()

	hash := envSnapshotHash()
	if lastProduct != "" && hash != lastEnvHash {
		telemetry.Count(telemetry.NamespaceGeneral, "config.repeat_start_env_diff", []string{
			"trigger_product:" + string(product),
			"previous_product:" + string(lastProduct),
		}).Submit(1)
	}
	lastEnvHash, lastProduct = hash, product
}

// envSnapshotHash covers the full supported-configuration surface, not just the
// calling product's keys, so it stays meaningful as more products migrate.
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
