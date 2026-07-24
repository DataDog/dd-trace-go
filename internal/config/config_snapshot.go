// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"reflect"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplingrules"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type preparedConfigReport struct {
	name     string
	prepared configtelemetry.Prepared
	value    any
	err      error
}

func prepareConfigReport(name string, value any, origin telemetry.Origin) preparedConfigReport {
	detached, err := prepareConfigTelemetryValue(value)
	return preparedConfigReport{
		name:     name,
		prepared: configtelemetry.Prepare(name, origin),
		value:    detached,
		err:      err,
	}
}

func prepareDefaultConfigReport(name string, value any) preparedConfigReport {
	detached, err := prepareConfigTelemetryValue(value)
	return preparedConfigReport{
		name:     name,
		prepared: configtelemetry.PrepareDefault(name),
		value:    detached,
		err:      err,
	}
}

func (r preparedConfigReport) submit() {
	if r.err != nil {
		log.Warn("config: unable to prepare %s telemetry: %v", r.name, r.err)
		return
	}
	r.prepared.Submit(telemetry.SanitizeConfigValue(r.value))
}

// prepareConfigTelemetryValue eagerly detaches the supported telemetry value
// shapes. It never invokes user-provided formatting or marshaling callbacks.
func prepareConfigTelemetryValue(value any) (any, error) {
	switch value := value.(type) {
	case nil:
		return nil, nil
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value, nil
	case time.Duration:
		// Match telemetry.SanitizeConfigValue's fmt.Stringer representation
		// without admitting arbitrary Stringer implementations.
		return value.String(), nil
	case []byte:
		return append([]byte(nil), value...), nil
	case []string:
		return append([]string(nil), value...), nil
	case []samplingrules.SamplingRule:
		return samplingrules.TelemetryString(value)
	case map[string]string:
		snapshot := make(map[string]string, len(value))
		for key, item := range value {
			snapshot[key] = item
		}
		return snapshot, nil
	case map[string]any:
		snapshot := make(map[string]any, len(value))
		for key, item := range value {
			detached, err := prepareConfigTelemetryValue(item)
			if err != nil {
				return nil, err
			}
			snapshot[key] = detached
		}
		return snapshot, nil
	case []any:
		snapshot := make([]any, len(value))
		for i, item := range value {
			detached, err := prepareConfigTelemetryValue(item)
			if err != nil {
				return nil, err
			}
			snapshot[i] = detached
		}
		return snapshot, nil
	}

	// Named scalar values are safe to detach through reflection because these
	// accessors do not invoke String, GoString, MarshalJSON, or other callbacks.
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	default:
		return nil, fmt.Errorf("unsupported configuration telemetry value type %T", value)
	}
}
