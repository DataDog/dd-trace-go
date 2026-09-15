// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import "testing"

func TestStateV3DocumentAdmissionBindsExactEntryAndExpires(t *testing.T) {
	entry := documentEntry(stateV3DocumentRecord, "requests/1/2/state.json", stateV3AssemblyMinor, 'a')
	if !entry.admission.permits(entry) {
		t.Fatal("valid admission rejected")
	}
	for name, alter := range map[string]func(*stateV3ApprovedDocumentEntry){
		"commit": func(entry *stateV3ApprovedDocumentEntry) { entry.commitOID = documentTestCommitOID },
		"tree":   func(entry *stateV3ApprovedDocumentEntry) { entry.treeOID = documentTestTreeOID },
		"path":   func(entry *stateV3ApprovedDocumentEntry) { entry.path = "requests/1/3/state.json" },
		"kind":   func(entry *stateV3ApprovedDocumentEntry) { entry.kind = stateV3DocumentBundle },
		"role":   func(entry *stateV3ApprovedDocumentEntry) { entry.role = stateV3AssemblyPatch },
		"index":  func(entry *stateV3ApprovedDocumentEntry) { entry.index++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := entry
			alter(&changed)
			if entry.admission.permits(changed) {
				t.Fatal("admission accepted a different entry")
			}
		})
	}
	entry.admission.owner.close()
	if entry.admission.permits(entry) {
		t.Fatal("closed admission remained usable")
	}
}
