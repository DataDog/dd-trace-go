// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// configuration is a data source that tracks SDK configuration key-value pairs
// (e.g. DD_ENV, DD_SERVICE) reported by products via RegisterAppConfig/RegisterAppConfigs.
//
// Flow:
//   - Products call Add() to register configs. Each config is stored in config and
//     its key is marked in pending.
//   - On each flush tick (~60s), the client calls Payload() which returns an
//     AppClientConfigurationChange containing only new/updated configs since the
//     last flush, then clears pending. The config map itself is never cleared,
//     so it accumulates the full state from app-started through any subsequent
//     config changes.
//   - When heartbeatEnricher emits an extended heartbeat (~24h), it calls All()
//     which returns the full accumulated config state from config. Since Add()
//     overwrites previous values for the same key, All() always reflects the
//     correct current state — merging startup configs with any subsequent updates.
//     This also ensures startup configs consumed by appStartedReducer (and
//     otherwise invisible to the mapper pipeline) are included.
//   - Both Payload() and All() normalize configs via normalize() which applies
//     default origin, sanitizes values, and assigns fallback seqIDs.
type configuration struct {
	mu sync.Mutex
	// config holds all registered configs for the lifetime of the SDK. Entries are
	// never removed; updated configs overwrite previous values for the same key.
	config map[configKey]transport.ConfKeyValue
	// pending tracks which keys in config have been added or updated since the last
	// Payload() call. Cleared after each flush so only deltas are reported.
	pending map[configKey]struct{}
	// fallbackSeqID is used only for legacy configs that don't already have a seqID.
	// New code should report configs with seqIDs via config/configProvider.
	fallbackSeqID uint64
}

type configKey struct {
	name   string
	origin string
}

func idOrEmpty(id string) string {
	if id == EmptyID {
		return ""
	}
	return id
}

func (c *configuration) Add(kv Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config == nil {
		c.config = make(map[configKey]transport.ConfKeyValue)
		c.pending = make(map[configKey]struct{})
	}

	ID := idOrEmpty(kv.ID)

	key := configKey{name: kv.Name, origin: string(kv.Origin)}
	c.config[key] = transport.ConfKeyValue{
		Name:   kv.Name,
		Value:  kv.Value,
		Origin: kv.Origin,
		ID:     ID,
		SeqID:  kv.SeqID,
	}
	c.pending[key] = struct{}{}
}

// normalize applies default origin, sanitizes the value, and assigns a fallback
// seqID if needed. The normalized conf is written back to c.config[key].
func (c *configuration) normalize(key configKey) transport.ConfKeyValue {
	conf := c.config[key]
	if conf.Origin == "" {
		conf.Origin = transport.OriginDefault
	}
	conf.Value = SanitizeConfigValue(conf.Value)
	if conf.SeqID == 0 {
		c.fallbackSeqID++
		conf.SeqID = c.fallbackSeqID
	}
	c.config[key] = conf
	return conf
}

func (c *configuration) Payload() transport.Payload {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return nil
	}

	configs := make([]transport.ConfKeyValue, 0, len(c.pending))
	for key := range c.pending {
		configs = append(configs, c.normalize(key))
	}
	clear(c.pending)

	return transport.AppClientConfigurationChange{
		Configuration: configs,
	}
}

// All returns a sanitized snapshot of all accumulated configs. Used by
// heartbeatEnricher to populate the configuration field in extended heartbeats.
func (c *configuration) All() []transport.ConfKeyValue {
	c.mu.Lock()
	defer c.mu.Unlock()

	configs := make([]transport.ConfKeyValue, 0, len(c.config))
	for key := range c.config {
		configs = append(configs, c.normalize(key))
	}
	return configs
}

// SanitizeConfigValue sanitizes the value of a configuration key to ensure it can be marshalled.
func SanitizeConfigValue(value any) any {
	if value == nil {
		return ""
	}

	// Skip reflection for basic types
	switch val := value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return val
	case float32:
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			return ""
		}
		return val
	case float64:
		// https://github.com/golang/go/issues/59627
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return nil
		}
		return val
	case []string:
		return strings.Join(val, ",") // Retro compatibility with old code
	}

	if _, ok := value.(json.Marshaler); ok {
		return value
	}

	if v, ok := value.(fmt.Stringer); ok {
		return v.String()
	}

	valueOf := reflect.ValueOf(value)

	// Unwrap pointers and interfaces up to 10 levels deep.
	for range 10 {
		if valueOf.Kind() == reflect.Pointer || valueOf.Kind() == reflect.Interface {
			valueOf = valueOf.Elem()
		} else {
			break
		}
	}

	switch {
	case valueOf.Kind() == reflect.Slice, valueOf.Kind() == reflect.Array:
		var sb strings.Builder
		sb.WriteString("[")
		for i := 0; i < valueOf.Len(); i++ {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(fmt.Sprintf("%v", valueOf.Index(i).Interface()))
		}
		sb.WriteString("]")
		return sb.String()
	case valueOf.Kind() == reflect.Map:
		kvPair := make([]struct {
			key   string
			value string
		}, valueOf.Len())

		iter := valueOf.MapRange()
		for i := 0; iter.Next(); i++ {
			kvPair[i].key = fmt.Sprintf("%v", iter.Key().Interface())
			kvPair[i].value = fmt.Sprintf("%v", iter.Value().Interface())
		}

		slices.SortStableFunc(kvPair, func(a, b struct {
			key   string
			value string
		}) int {
			return strings.Compare(a.key, b.key)
		})

		var sb strings.Builder
		for _, k := range kvPair {
			if sb.Len() > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(k.key)
			sb.WriteString(":")
			sb.WriteString(k.value)
		}

		return sb.String()
	}

	return fmt.Sprintf("%v", value)
}

func EnvToTelemetryName(env string) string {
	switch env {
	case "DD_TRACE_DEBUG":
		return "trace_debug_enabled"
	case "DD_APM_TRACING_ENABLED":
		return "apm_tracing_enabled"
	case "DD_RUNTIME_METRICS_ENABLED":
		return "runtime_metrics_enabled"
	case "DD_DATA_STREAMS_ENABLED":
		return "data_streams_enabled"
	case "DD_APPSEC_ENABLED":
		return "appsec_enabled"
	case "DD_DYNAMIC_INSTRUMENTATION_ENABLED":
		return "dynamic_instrumentation_enabled"
	case "DD_PROFILING_ENABLED":
		return "profiling_enabled"
	default:
		return env
	}
}
