// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func (p *profiler) sendTelemetry() {
	if telemetry.Disabled() {
		return
	}
	configs := []telemetry.Configuration{}
	telemetry.GlobalClient.ProductEnabled(telemetry.NamespaceProfilers,
		true,
		append(configs, []telemetry.Configuration{
			{Name: "delta_profiles", Value: p.cfg.deltaProfiles},
			{Name: "agentless", Value: p.cfg.agentless},
			{Name: "profile_period", Value: p.cfg.period.String()},
			{Name: "cpu_duration", Value: p.cfg.cpuDuration.String()},
			{Name: "cpu_profile_rate", Value: p.cfg.cpuProfileRate},
			{Name: "block_profile_rate", Value: p.cfg.blockRate},
			{Name: "mutex_profile_fraction", Value: p.cfg.mutexFraction},
			{Name: "max_goroutines_wait", Value: p.cfg.maxGoroutinesWait},
			{Name: "cpu_profile_enabled", Value: p.profileEnabled(CPUProfile)},
			{Name: "heap_profile_enabled", Value: p.profileEnabled(HeapProfile)},
			{Name: "block_profile_enabled", Value: p.profileEnabled(BlockProfile)},
			{Name: "mutex_profile_enabled", Value: p.profileEnabled(MutexProfile)},
			{Name: "goroutine_profile_enabled", Value: p.profileEnabled(GoroutineProfile)},
			{Name: "goroutine_wait_profile_enabled", Value: p.profileEnabled(expGoroutineWaitProfile)},
			{Name: "upload_timeout", Value: p.cfg.uploadTimeout.String()},
		}...))
}
