package namingschema

import (
	"os"
	"sync"
)

const (
	envSpanAttributeSchema = "DD_TRACE_SPAN_ATTRIBUTE_SCHEMA"
)

// Version represents the available naming schema versions.
type Version int

const (
	SchemaV0 Version = iota
	SchemaV1
)

var (
	mu sync.RWMutex
	sv Version
)

// TODO: probably this can be moved to ddtrace/tracer/option.go
func init() {
	mu.Lock()
	defer mu.Unlock()
	switch os.Getenv(envSpanAttributeSchema) {
	case "", "v0":
		sv = SchemaV0
	case "v1":
		sv = SchemaV1
	default:
		// TODO: log warning "unknown value for DD_TRACE_SPAN_ATTRIBUTE_SCHEMA"
		sv = SchemaV0
	}
}

func GetVersion() Version {
	mu.RLock()
	defer mu.RUnlock()
	return sv
}

func SetVersion(v Version) {
	mu.Lock()
	defer mu.Unlock()
	sv = v
}

// VersionSupportSchema is an interface that ensures all the available naming schema versions are implemented by the caller.
type VersionSupportSchema interface {
	V0() string
	V1() string
}

type Schema interface {
	VersionSupportSchema
	GetName() string
}

type schema struct {
	vSchema VersionSupportSchema
}

func New(vSchema VersionSupportSchema) Schema {
	return &schema{vSchema: vSchema}
}

func (s *schema) V0() string {
	return s.vSchema.V0()
}

func (s *schema) V1() string {
	return s.vSchema.V1()
}

func (s *schema) GetName() string {
	return GetName(s)
}

func GetName(vSchema VersionSupportSchema) string {
	mu.RLock()
	defer mu.RUnlock()
	switch sv {
	case SchemaV1:
		return vSchema.V1()
	default:
		return vSchema.V0()
	}
}
