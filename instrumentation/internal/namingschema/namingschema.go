package namingschema

import (
	"os"
	"strings"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type Version int

const (
	VersionV0 Version = iota
	VersionV1
)

var (
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
	removeFakeServiceNames = env.BoolEnv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", false)
}

func GetConfig() Config {
	if testMode == nil {
		v := env.BoolEnv("__DD_TRACE_NAMING_SCHEMA_TEST", false)
		testMode = &v
	}
	if *testMode {
		LoadFromEnv()
		if ddService := os.Getenv("DD_SERVICE"); ddService != "" {
			globalconfig.SetServiceName(ddService)
		}
	}
	return Config{
		NamingSchemaVersion:    Version(atomic.LoadInt32(&activeNamingSchema)),
		RemoveFakeServiceNames: removeFakeServiceNames,
		DDService:              globalconfig.ServiceName(),
	}
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
