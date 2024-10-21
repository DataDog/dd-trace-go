// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"reflect"
	"testing"
	"unsafe"
)

// testDepsCoverage is an interface to support runtime coverage initialization from the original testDeps testing interface
type testDepsCoverage interface {
	InitRuntimeCoverage() (mode string, tearDown func(coverprofile string, gocoverdir string) (string, error), snapcov func() float64)
}

//go:linkname getFieldPointerFrom gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting.getFieldPointerFrom
func getFieldPointerFrom(value any, fieldName string) (unsafe.Pointer, error)

// getTestDepsCoverage gets the testDepsCoverage interface from a testing.M instance
func getTestDepsCoverage(m *testing.M) (testDepsCoverage, error) {
	ptr, err := getFieldPointerFrom(m, "deps")
	if err != nil {
		return nil, err
	}

	tDepValue := reflect.NewAt(reflect.TypeFor[testDepsCoverage](), ptr)
	return tDepValue.Elem().Interface().(testDepsCoverage), nil
}
