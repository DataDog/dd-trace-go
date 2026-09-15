// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"reflect"
	"testing"
	"unsafe"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3DocumentStoreFixedTopology(t *testing.T) {
	var store stateV3DocumentStore
	if stateV3StoreMaxBlobSlots != gardenerrelease.MaxStateV3DocumentBlobVersions || stateV3StoreRawBytes != gardenerrelease.MaxStateV3DocumentStoreRawBytes {
		t.Fatal("store capacity is not derived from the authoritative topology")
	}
	if len(store.blobs) != stateV3StoreMaxBlobSlots || len(store.bindings) != stateV3StoreMaxBindings || len(store.raw) != stateV3StoreRawBytes {
		t.Fatal("unexpected fixed store topology")
	}
	storeType := reflect.TypeOf((*stateV3DocumentStore)(nil)).Elem()
	for i := 0; i < storeType.NumField(); i++ {
		field := storeType.Field(i)
		typ := field.Type
		if typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice || typ.Kind() == reflect.String || typ.Kind() == reflect.Interface || typ.Kind() == reflect.Pointer {
			t.Fatalf("retained dynamic field %s", field.Name)
		}
	}
	if unsafe.Sizeof(store) < uintptr(stateV3StoreRawBytes)+unsafe.Sizeof(store.blobs)+unsafe.Sizeof(store.bindings) {
		t.Fatal("store does not structurally own every fixed arena")
	}
	if unsafe.Sizeof(stateV3BlobSlot{}.oid) != 20 || unsafe.Sizeof(stateV3BlobSlot{}.digest) != 32 || unsafe.Sizeof(stateV3BindingSlot{}.commitOID) != 20 || unsafe.Sizeof(stateV3BindingSlot{}.treeOID) != 20 {
		t.Fatal("identifiers are not fixed binary values")
	}
}
