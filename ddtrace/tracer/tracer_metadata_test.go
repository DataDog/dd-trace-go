// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToProcessContextFieldMapping(t *testing.T) {
	m := Metadata{
		RuntimeID:          "runtime-id-123",
		Language:           "go",
		Version:            "v1.2.3",
		Hostname:           "my-host",
		ServiceName:        "my-service",
		ServiceEnvironment: "production",
		ServiceVersion:     "2.0.0",
		ProcessTags:        "tag1=value1,tag2=value2",
		ContainerID:        "container-id-123",
	}

	pc := m.toProcessContext()
	require.NotNil(t, pc)
	require.NotNil(t, pc.GetResource())

	// Build a map from the proto attributes for easy lookup.
	attrs := make(map[string]string)
	for _, kv := range pc.GetResource().GetAttributes() {
		attrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}

	extraAttrs := make(map[string]string)
	for _, kv := range pc.GetExtraAttributes() {
		extraAttrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}

	require.Equal(t, map[string]string{
		"service.instance.id":         m.RuntimeID,
		"telemetry.sdk.language":      m.Language,
		"telemetry.sdk.version":       m.Version,
		"host.name":                   m.Hostname,
		"service.name":                m.ServiceName,
		"deployment.environment.name": m.ServiceEnvironment,
		"service.version":             m.ServiceVersion,
		"telemetry.sdk.name":          "dd-trace-go",
		"container.id":                m.ContainerID,
	}, attrs)
	require.Equal(t, map[string]string{
		"datadog.process_tags": m.ProcessTags,
	}, extraAttrs)
}

// TestToProcessContextCoversAllMetadataStringFields uses reflection to verify
// that every string field of Metadata is represented in the ProcessContext
// output. If a new string field is added to Metadata without updating
// toProcessContext, this test will fail.
func TestToProcessContextCoversAllMetadataStringFields(t *testing.T) {
	// Populate every string field with a unique sentinel value.
	m := Metadata{}
	rv := reflect.ValueOf(&m).Elem()
	rt := rv.Type()

	for i := range rt.NumField() {
		if f := rv.Field(i); f.Kind() == reflect.String {
			f.SetString("value-" + rt.Field(i).Name)
		}
	}

	pc := m.toProcessContext()

	// Collect every string value present in the output.
	got := make(map[string]bool)
	for _, kv := range pc.GetResource().GetAttributes() {
		got[kv.GetValue().GetStringValue()] = true
	}
	for _, kv := range pc.GetExtraAttributes() {
		got[kv.GetValue().GetStringValue()] = true
	}

	// Assert that every string field's sentinel value appears in the output.
	for i := range rt.NumField() {
		f := rv.Field(i)
		if f.Kind() != reflect.String {
			continue
		}
		name := rt.Field(i).Name
		require.True(t, got[f.String()],
			"Metadata.%s (value %q) not found in ProcessContext", name, f.String())
	}
}
