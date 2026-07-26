// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschema

import (
	"strings"
	"sync"
	"sync/atomic"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
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
	activeNamingSchema     atomic.Int32
	removeFakeServiceNames bool
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
	loadFromConfig(internalconfig.Get())
}

func loadFromConfig(cfg *internalconfig.Config) {
	schemaVersion := cfg.RawSpanAttributeSchema()
	if version, ok := parseVersionString(schemaVersion); ok {
		setVersion(version)
	} else {
		setVersion(VersionV0)
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, setting to default of v%d", schemaVersion, VersionV0)
	}
	// Allow DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=v0 users to disable default integration (contrib AKA v0) service names.
	// These default service names are always disabled for v1 onwards.
	removeFakeServiceNames = cfg.RemoveIntegrationServiceNames()
}

func ReloadConfig() {
	cfg := internalconfig.Get()
	loadFromConfig(cfg)
	globalconfig.SetServiceName(cfg.RawServiceName())
}

func GetConfig() Config {
	mu.Lock()
	defer mu.Unlock()

	return Config{
		NamingSchemaVersion:    GetVersion(),
		RemoveFakeServiceNames: removeFakeServiceNames,
		DDService:              globalconfig.ServiceName(),
	}
}

func GetVersion() Version {
	return Version(activeNamingSchema.Load())
}

func setVersion(v Version) {
	activeNamingSchema.Store(int32(v))
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
