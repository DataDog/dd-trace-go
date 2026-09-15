// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func documentKey(kind stateV3DocumentKind, raw []byte, tail byte) stateV3DocumentKey {
	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + string(tail)
	digest := sha256.Sum256(raw)
	return stateV3DocumentKey{oid: oid, digest: hex.EncodeToString(digest[:]), kind: kind}
}

func documentEntry(kind stateV3DocumentKind, path string, role stateV3AssemblyRole, tail byte) stateV3ApprovedDocumentEntry {
	entry := stateV3ApprovedDocumentEntry{
		kind:      kind,
		path:      path,
		role:      role,
		oid:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + string(tail),
		commitOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + string(tail),
		treeOID:   "ccccccccccccccccccccccccccccccccccccccc" + string(tail),
	}
	entry.admission = testDocumentAdmission(entry)
	return entry
}

// testDocumentAdmission constructs a capability literal only in test code.
// Production capabilities are issued exclusively by admitCompact from the
// session-owned authenticated compact history.
func testDocumentAdmission(entry stateV3ApprovedDocumentEntry) stateV3DocumentAdmission {
	commitOID, commitOK := decodeFixedOID(entry.commitOID)
	treeOID, treeOK := decodeFixedOID(entry.treeOID)
	blobOID, blobOK := decodeFixedOID(entry.oid)
	if !commitOK || !treeOK || !blobOK {
		panic("invalid test document identity")
	}
	owner := &stateV3LaneHistoryAdmission{child: &stateV3AssemblyChild{session: &session{assemblyRole: entry.role}}}
	return stateV3DocumentAdmission{
		owner: owner, role: entry.role, commitOID: commitOID, treeOID: treeOID,
		index: entry.index, kind: entry.kind, path: entry.path, blobOID: blobOID,
	}
}

func TestStateV3DocumentStoreOwnsAndCloses(t *testing.T) {
	store := &stateV3DocumentStore{}
	raw := []byte("bundle")
	key := documentKey(stateV3DocumentBundle, raw, 'a')
	if !store.putEntry(key, documentEntry(key.kind, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a'), raw) {
		t.Fatal("initial put")
	}
	raw[0] = 'x'
	got, ok := store.get(key)
	if !ok || string(got.raw) != "bundle" {
		t.Fatal("store aliases input")
	}
	got.raw[0] = 'y'
	got, ok = store.get(key)
	if !ok || string(got.raw) != "bundle" {
		t.Fatal("get aliases retained data")
	}
	store.close()
	if store.blobCount != 0 || store.bindingCount != 0 || store.rawUsed != 0 || store.putEntry(key, documentEntry(key.kind, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a'), []byte("bundle")) {
		t.Fatal("close did not terminally clear store")
	}
}

func TestStateV3DocumentStoreRejectsInvalidAndRawOverflow(t *testing.T) {
	store := &stateV3DocumentStore{rawUsed: stateV3StoreRawBytes}
	key := documentKey(stateV3DocumentBundle, []byte("x"), 'a')
	if store.putEntry(key, documentEntry(key.kind, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a'), []byte("x")) {
		t.Fatal("accepted raw arena overflow")
	}
	if (&stateV3DocumentStore{}).putEntry(stateV3DocumentKey{oid: "bad", digest: "bad", kind: stateV3DocumentBundle}, documentEntry(stateV3DocumentBundle, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a'), []byte("x")) {
		t.Fatal("accepted invalid fixed identifiers")
	}
}

func TestStateV3DocumentStoreFullArenaAcceptsNewBindingForRetainedBlob(t *testing.T) {
	raw := []byte("record")
	key := documentKey(stateV3DocumentRecord, raw, 'a')
	first := documentEntry(key.kind, "requests/1/2/state.json", stateV3AssemblyMinor, 'a')
	store := &stateV3DocumentStore{rawUsed: stateV3StoreRawBytes, blobCount: 1}
	oid, _ := decodeFixedOID(key.oid)
	digest, _ := decodeFixedDigest(key.digest)
	store.blobs[0] = stateV3BlobSlot{oid: oid, digest: digest, kind: key.kind, rawLen: uint32(len(raw)), used: true}
	copy(store.raw[:], raw)
	second := documentEntry(key.kind, "requests/1/2/state.json", stateV3AssemblyMinor, 'b')
	second.treeOID = first.treeOID
	second.admission = testDocumentAdmission(second)
	if !store.putEntry(key, second, raw) {
		t.Fatal("full raw arena rejected new binding for retained blob")
	}
	if store.rawUsed != stateV3StoreRawBytes || store.blobCount != 1 || store.bindingCount != 1 {
		t.Fatalf("unexpected counts raw=%d blobs=%d bindings=%d", store.rawUsed, store.blobCount, store.bindingCount)
	}
}
