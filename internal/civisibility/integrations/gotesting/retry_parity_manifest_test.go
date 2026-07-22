// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

var retryParityCoveredMethods = map[string]struct{}{
	"ArtifactDir": {},
	"Attr":        {},
	"Chdir":       {},
	"Cleanup":     {},
	"Context":     {},
	"Deadline":    {},
	"Error":       {},
	"Errorf":      {},
	"Fail":        {},
	"FailNow":     {},
	"Failed":      {},
	"Fatal":       {},
	"Fatalf":      {},
	"Helper":      {},
	"Log":         {},
	"Logf":        {},
	"Name":        {},
	"Output":      {},
	"Parallel":    {},
	"Run":         {},
	"Setenv":      {},
	"Skip":        {},
	"SkipNow":     {},
	"Skipf":       {},
	"Skipped":     {},
	"TempDir":     {},
}

func TestProcessRetryParityManifestCoversSupportedMethodSurface(t *testing.T) {
	for _, method := range retryParityConcreteMethodNames((*testing.T)(nil)) {
		_, ok := retryParityCoveredMethods[method]
		require.True(t, ok, "testing.T.%s has no parity scenario", method)
	}
	for _, method := range retryParityInterfaceMethodNames((*testing.TB)(nil)) {
		if _, ok := retryParityCoveredMethods[method]; ok {
			continue
		}
		require.Equal(t, "private", method, "testing.TB.%s is unclassified", method)
	}
	for _, method := range retryParityConcreteMethodNames((*T)(nil)) {
		_, ok := retryParityCoveredMethods[method]
		require.True(t, ok, "gotesting.T.%s has no parity scenario", method)
	}
}

func retryParityConcreteMethodNames(value any) []string {
	typ := reflect.TypeOf(value)
	methods := make([]string, 0, typ.NumMethod())
	for index := 0; index < typ.NumMethod(); index++ {
		methods = append(methods, typ.Method(index).Name)
	}
	slices.Sort(methods)
	return methods
}

func retryParityInterfaceMethodNames(value any) []string {
	typ := reflect.TypeOf(value).Elem()
	methods := make([]string, 0, typ.NumMethod())
	for index := 0; index < typ.NumMethod(); index++ {
		methods = append(methods, typ.Method(index).Name)
	}
	slices.Sort(methods)
	return methods
}
