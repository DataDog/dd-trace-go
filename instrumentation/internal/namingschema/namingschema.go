// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

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

type Version int

const (
	VersionV0 Version = iota
	VersionV1
)

var (
	mu                     sync.Mutex
	activeNamingSchema     int32
	removeFakeServiceNames bool
	testMode               *bool
)

type Config struct {
	NamingSchemaVersion    Version
	RemoveFakeServiceNames bool
	DDService              string
}

func init() {
	LoadFromEnv()
}

func LoadFromEnv() {
	schemaVersionStr := os.Getenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA")
	if v, ok := parseVersionString(schemaVersionStr); ok {
		setVersion(v)
	} else {
		setVersion(VersionV0)
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, setting to default of v%d", schemaVersionStr, v)
	}
	// Allow DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=v0 users to disable default integration (contrib AKA v0) service names.
	// These default service names are always disabled for v1 onwards.
	removeFakeServiceNames = internal.BoolEnv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", false)
}

func GetConfig() Config {
	mu.Lock()
	defer mu.Unlock()

	if testMode == nil {
		v := internal.BoolEnv("__DD_TRACE_NAMING_SCHEMA_TEST", false)
		testMode = &v
	}
	if *testMode {
		LoadFromEnv()
		globalconfig.SetServiceName(os.Getenv("DD_SERVICE"))
	}
	return Config{
		NamingSchemaVersion:    GetVersion(),
		RemoveFakeServiceNames: removeFakeServiceNames,
		DDService:              globalconfig.ServiceName(),
	}
}

func GetVersion() Version {
	return Version(atomic.LoadInt32(&activeNamingSchema))
}

func setVersion(v Version) {
	atomic.StoreInt32(&activeNamingSchema, int32(v))
}

func parseVersionString(v string) (Version, bool) {
	switch strings.ToLower(v) {
	case "", "v0":
		return VersionV0, true
	case "v1":
		return VersionV1, true
	default:
		return VersionV0, false
	}
}
