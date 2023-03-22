// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import (
	"os"
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

func init() {
	mu.Lock()
	defer mu.Unlock()
	switch version := strings.ToLower(os.Getenv(envSpanAttributeSchema)); version {
	case "", "v0":
		sv = SchemaV0
	case "v1":
		sv = SchemaV1
	default:
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, setting to default of v0", version)
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

type Schema struct {
	selectedVersion Version
	vSchema         VersionSupportSchema
}

func New(vSchema VersionSupportSchema) *Schema {
	return &Schema{selectedVersion: GetVersion(), vSchema: vSchema}
}

func (s *Schema) GetName() string {
	switch s.selectedVersion {
	case SchemaV1:
		return s.vSchema.V1()
	default:
		return s.vSchema.V0()
	}
}
