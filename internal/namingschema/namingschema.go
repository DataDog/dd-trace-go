// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package namingschema allows to use the naming schema from the integrations to set different
// service and span/operation names based on the value of the DD_TRACE_SPAN_ATTRIBUTE_SCHEMA environment variable.
package namingschema

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Version represents the available naming schema versions.
type Version int

const (
	// VersionV0 represents naming schema v0.
	VersionV0 Version = iota
	// VersionV1 represents naming schema v1.
	VersionV1
)

type Config struct {
	NamingSchemaVersion    Version
	RemoveFakeServiceNames bool
	DDService              string
}

var (
	activeNamingSchema     int32
	removeFakeServiceNames bool
	mu                     sync.RWMutex
)

// GetVersion returns the global naming schema version used for this application.
func GetVersion() Version {
	return Version(atomic.LoadInt32(&activeNamingSchema))
}

// SetRemoveFakeServices is equivalent to the DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED environment variable.
func SetRemoveFakeServices(v bool) {
	mu.Lock()
	defer mu.Unlock()
	removeFakeServiceNames = v
}

func LoadFromEnv() {
	schemaVersionStr := os.Getenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA")
	if v, ok := parseVersionStr(schemaVersionStr); ok {
		setVersion(v)
	} else {
		setVersion(VersionV0)
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, setting to default of v%d", schemaVersionStr, v)
	}
	// Allow DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=v0 users to disable default integration (contrib AKA v0) service names.
	// These default service names are always disabled for v1 onwards.
	removeFakeServiceNames = internal.BoolEnv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", false)
}

// ReloadConfig is used to reload the configuration in tests.
func ReloadConfig() {
	LoadFromEnv()
	globalconfig.SetServiceName(os.Getenv("DD_SERVICE"))
}

// GetConfig returns the naming schema config.
func GetConfig() Config {
	mu.Lock()
	defer mu.Unlock()

	return Config{
		NamingSchemaVersion:    GetVersion(),
		RemoveFakeServiceNames: removeFakeServiceNames,
		DDService:              globalconfig.ServiceName(),
	}
}

// setVersion sets the global naming schema version used for this application.
func setVersion(v Version) {
	atomic.StoreInt32(&activeNamingSchema, int32(v))
}

// parseVersionStr attempts to parse the version string.
func parseVersionStr(v string) (Version, bool) {
	switch strings.ToLower(v) {
	case "", "v0":
		return VersionV0, true
	case "v1":
		return VersionV1, true
	default:
		return VersionV0, false
	}
}
