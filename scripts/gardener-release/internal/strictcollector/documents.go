// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strings"
	"sync"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

type stateV3DocumentKind uint8

const (
	stateV3DocumentLease stateV3DocumentKind = iota + 1
	stateV3DocumentRecord
	stateV3DocumentReservation
	stateV3DocumentPrepared
	stateV3DocumentBundle
	stateV3DocumentCoordinationArm
	stateV3DocumentCoordinationClaim
	stateV3DocumentCoordinationOutcome
)

type stateV3ApprovedDocumentEntry struct {
	index     int
	kind      stateV3DocumentKind
	path      string
	oid       string
	commitOID string
	treeOID   string
	role      stateV3AssemblyRole
	admission stateV3DocumentAdmission
}

type stateV3DocumentKey struct {
	oid, digest string
	kind        stateV3DocumentKind
}
type stateV3ImmutableDocument struct{ raw []byte }

const (
	stateV3StoreMaxBlobSlots = gardenerrelease.MaxStateV3DocumentBlobVersions
	stateV3StoreMaxBindings  = gardenerrelease.MaxStateV3DocumentProvenanceBindings
	stateV3StoreRawBytes     = gardenerrelease.MaxStateV3DocumentStoreRawBytes
	stateV3StoredPathBytes   = 128
)

type stateV3BlobSlot struct {
	oid    [20]byte
	digest [32]byte
	kind   stateV3DocumentKind
	rawAt  uint32
	rawLen uint32
	used   bool
}
type stateV3BindingSlot struct {
	commitOID [20]byte
	treeOID   [20]byte
	path      [stateV3StoredPathBytes]byte
	pathLen   uint8
	role      stateV3AssemblyRole
	blobSlot  uint16
	used      bool
}
type stateV3DocumentStore struct {
	mu           sync.Mutex
	blobs        [stateV3StoreMaxBlobSlots]stateV3BlobSlot
	bindings     [stateV3StoreMaxBindings]stateV3BindingSlot
	raw          [stateV3StoreRawBytes]byte
	blobCount    uint16
	bindingCount uint16
	rawUsed      uint32
	closed       bool
}

const (
	maxStateV3LaneHistorySnapshots         = gardenerrelease.MaxStateV3StateLaneHistoryCommits
	maxStateV3CoordinationHistorySnapshots = gardenerrelease.MaxStateV3CoordinationHistoryCommits
	maxStateV3DocumentBindings             = stateV3StoreMaxBindings
	maxStateV3Documents                    = stateV3StoreMaxBlobSlots
	maxStateV3Leases                       = gardenerrelease.MaxStateV3LaneOperationWindows
	maxStateV3Reservations                 = gardenerrelease.MaxStateV3LaneOperationWindows
	maxStateV3PreparedManifests            = gardenerrelease.MaxStateV3LaneOperationWindows
	maxStateV3Bundles                      = gardenerrelease.MaxStateV3LaneOperationWindows
	maxStateV3CoordinationDocuments        = gardenerrelease.MaxStateV3CoordinationDocumentBindings
	maxStateV3DocumentStoreBytes           = stateV3StoreRawBytes
)

func (c *stateV3AssemblyChild) approveDocument(rawCommit, tree handle, kind stateV3DocumentKind) (stateV3ApprovedDocumentEntry, Result) {
	if c == nil || c.session == nil || !c.session.assemblyDocumentRoleAllowed(kind) {
		return stateV3ApprovedDocumentEntry{}, failure(DiagnosticProtocol)
	}
	entries, result := c.approveDocuments(rawCommit, tree, kind)
	if result.Diagnostic != DiagnosticOK {
		return stateV3ApprovedDocumentEntry{}, result
	}
	if len(entries) != 1 {
		return stateV3ApprovedDocumentEntry{}, failure(DiagnosticResponseInvalid)
	}
	return entries[0], Result{}
}

// approveDocuments returns only the complete, bounded set for one typed kind.
// It is private to the strictcollector authority package; no candidate list
// crosses its package boundary.
func (c *stateV3AssemblyChild) approveDocuments(rawCommit, tree handle, kind stateV3DocumentKind) ([]stateV3ApprovedDocumentEntry, Result) {
	if c == nil || c.session == nil || !c.session.assemblyDocumentRoleAllowed(kind) {
		return nil, failure(DiagnosticProtocol)
	}
	commit, commitOK := c.session.rawCommitFor(rawCommit)
	value, treeOK := c.session.treeFor(tree)
	if !commitOK || !treeOK || !validOID(commit.SHA) || !validOID(commit.Tree.SHA) || commit.Tree.SHA != value.SHA {
		return nil, failure(DiagnosticProtocol)
	}
	all, valid := stateV3DiscoverDocumentEntries(c.session.assemblyRole, value.Tree)
	if !valid {
		return nil, failure(DiagnosticResponseInvalid)
	}
	entries := make([]stateV3ApprovedDocumentEntry, 0, 2)
	for _, entry := range all {
		if entry.kind == kind {
			entry.treeOID = strings.Clone(value.SHA)
			entry.commitOID = strings.Clone(commit.SHA)
			entry.role = c.session.assemblyRole
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil, failure(DiagnosticRequiredEvidenceAbsent)
	}
	return entries, Result{}
}

func stateV3DiscoverDocumentEntries(role stateV3AssemblyRole, treeEntries []wireTreeEntry) ([]stateV3ApprovedDocumentEntry, bool) {
	entries := make([]stateV3ApprovedDocumentEntry, 0, 5)
	seenKinds := make(map[stateV3DocumentKind]bool)
	claimPaths := make(map[string]bool)
	requestRoot := ""
	for index, value := range treeEntries {
		// Recursive Git tree responses include directory entries in addition to
		// leaf blobs. Directories carry no document authority and are ignored;
		// every retained leaf remains an approved regular blob.
		if value.Type == "tree" && value.Mode == "040000" {
			continue
		}
		if value.Type != "blob" || value.Mode != "100644" || !validOID(value.SHA) {
			return nil, false
		}
		kind, root, ok := stateV3DocumentEntryKind(role, value.Path)
		if !ok {
			return nil, false
		}
		if root != "" {
			if requestRoot == "" {
				requestRoot = root
			} else if requestRoot != root {
				return nil, false
			}
		}
		if kind == stateV3DocumentCoordinationClaim {
			if claimPaths[value.Path] || len(claimPaths) == 2 {
				return nil, false
			}
			claimPaths[value.Path] = true
		} else if seenKinds[kind] {
			return nil, false
		}
		seenKinds[kind] = true
		entries = append(entries, stateV3ApprovedDocumentEntry{index: index, kind: kind, path: strings.Clone(value.Path), oid: strings.Clone(value.SHA), role: role})
	}
	if role == stateV3AssemblyCoordination {
		if seenKinds[stateV3DocumentCoordinationArm] && seenKinds[stateV3DocumentCoordinationOutcome] || len(entries) > gardenerrelease.MaxStateV3CoordinationReleaseProofs+1 {
			return nil, false
		}
	}
	if role == stateV3AssemblyMinor || role == stateV3AssemblyPatch {
		lease, record := seenKinds[stateV3DocumentLease], seenKinds[stateV3DocumentRecord]
		envelope := seenKinds[stateV3DocumentReservation] || seenKinds[stateV3DocumentPrepared] || seenKinds[stateV3DocumentBundle]
		fullEnvelope := seenKinds[stateV3DocumentReservation] && seenKinds[stateV3DocumentPrepared] && seenKinds[stateV3DocumentBundle]
		if !(len(entries) == 0 || (lease && !record && !envelope && len(entries) == 1) || (lease && record && !envelope && len(entries) == 2) || (lease && record && fullEnvelope && len(entries) == 5)) {
			return nil, false
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, true
}

// stateV3DiscoverDocumentEntries validates the complete control-file shape
// before a single tree entry becomes usable. It deliberately has no
// first-match behavior: duplicate kinds, distinct request roots, and every
// unrecognised regular leaf fail closed.

func stateV3DocumentEntryKind(role stateV3AssemblyRole, path string) (stateV3DocumentKind, string, bool) {
	if role == stateV3AssemblyCoordination {
		if path == "mutation-arm.json" {
			return stateV3DocumentCoordinationArm, "", true
		}
		if gardenerrelease.ValidateStateV3CoordinationClaimPath(path) {
			return stateV3DocumentCoordinationClaim, "", true
		}
		if path == gardenerrelease.StateV3CoordinationMutationOutcomePath {
			return stateV3DocumentCoordinationOutcome, "", true
		}
		return 0, "", false
	}
	if role != stateV3AssemblyMinor && role != stateV3AssemblyPatch {
		return 0, "", false
	}
	if path == gardenerrelease.StateV3ActiveLeasePath {
		return stateV3DocumentLease, "", true
	}
	if !gardenerrelease.ValidateStateV3StateDocumentPath(path) {
		return 0, "", false
	}
	parts := strings.Split(path, "/")
	root := strings.Join(parts[:3], "/")
	switch parts[3] {
	case "state.json":
		return stateV3DocumentRecord, root, true
	case "reservation.json":
		return stateV3DocumentReservation, root, true
	case "prepared.json":
		return stateV3DocumentPrepared, root, true
	case "generation.bundle":
		return stateV3DocumentBundle, root, true
	}
	return 0, "", false
}

func (s *session) assemblyDocumentRoleAllowed(kind stateV3DocumentKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.assemblyLive {
		return false
	}
	if s.assemblyRole == stateV3AssemblyCoordination {
		return kind == stateV3DocumentCoordinationArm || kind == stateV3DocumentCoordinationClaim || kind == stateV3DocumentCoordinationOutcome
	}
	return kind >= stateV3DocumentLease && kind <= stateV3DocumentBundle
}

func stateV3DocumentPathAllowed(role stateV3AssemblyRole, kind stateV3DocumentKind, path string) bool {
	actual, _, ok := stateV3DocumentEntryKind(role, path)
	return ok && actual == kind
}
func stateV3DocumentValid(kind stateV3DocumentKind, path string, raw []byte) bool {
	limit := map[stateV3DocumentKind]int{stateV3DocumentLease: gardenerrelease.MaxStateV3ActiveLeaseBytes, stateV3DocumentRecord: gardenerrelease.MaxStateV3StateRecordBytes, stateV3DocumentReservation: gardenerrelease.MaxStateV3ReservationBytes, stateV3DocumentPrepared: gardenerrelease.MaxStateV3PreparedManifestBytes, stateV3DocumentBundle: gardenerrelease.MaxStateV3PreparedBundleBytes, stateV3DocumentCoordinationArm: gardenerrelease.MaxStateV3CoordinationArmBytes, stateV3DocumentCoordinationClaim: gardenerrelease.MaxStateV3CoordinationClaimBytes, stateV3DocumentCoordinationOutcome: gardenerrelease.MaxStateV3CoordinationOutcomeBytes}[kind]
	if len(raw) == 0 || len(raw) > limit {
		return false
	}
	switch kind {
	case stateV3DocumentLease:
		return gardenerrelease.ValidateStateV3ActiveLeaseDocument(raw)
	case stateV3DocumentRecord:
		return gardenerrelease.ValidateStateV3RecordDocumentPath(raw, path)
	case stateV3DocumentReservation:
		return gardenerrelease.ValidateStateV3ReservationDocumentPath(raw, path)
	case stateV3DocumentPrepared:
		return gardenerrelease.ValidateStateV3PreparedManifestDocument(raw)
	case stateV3DocumentCoordinationArm:
		return gardenerrelease.ValidateStateV3CoordinationArmDocument(raw)
	case stateV3DocumentCoordinationClaim:
		return gardenerrelease.ValidateStateV3ReleaseLineClaimDocumentPath(raw, path)
	case stateV3DocumentCoordinationOutcome:
		return gardenerrelease.ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw, path)
	case stateV3DocumentBundle:
		return true
	}
	return false
}
func decodeFixedOID(value string) ([20]byte, bool) {
	var result [20]byte
	if !validOID(value) {
		return result, false
	}
	_, err := hex.Decode(result[:], []byte(value))
	return result, err == nil
}

func decodeFixedDigest(value string) ([32]byte, bool) {
	var result [32]byte
	if len(value) != 64 {
		return result, false
	}
	_, err := hex.Decode(result[:], []byte(value))
	return result, err == nil
}

// putEntry is the sole document-store insertion point. It accepts only a
// fully approved, commit-bound entry and validates every retained field before
// changing either arena or either slot array.
func (s *stateV3DocumentStore) putEntry(key stateV3DocumentKey, entry stateV3ApprovedDocumentEntry, raw []byte) bool {
	keyOID, keyOIDOK := decodeFixedOID(key.oid)
	keyDigest, keyDigestOK := decodeFixedDigest(key.digest)
	commitOID, commitOK := decodeFixedOID(entry.commitOID)
	treeOID, treeOK := decodeFixedOID(entry.treeOID)
	bindingOK := entry.admission.permits(entry) && keyOIDOK && keyDigestOK && commitOK && treeOK && entry.kind == key.kind && entry.role >= stateV3AssemblyMinor && entry.role <= stateV3AssemblyCoordination && stateV3DocumentPathAllowed(entry.role, entry.kind, entry.path) && len(entry.path) <= stateV3StoredPathBytes && len(raw) > 0
	if !bindingOK {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	blob := -1
	for i := 0; i < int(s.blobCount); i++ {
		candidate := &s.blobs[i]
		if candidate.kind == key.kind && candidate.oid == keyOID && candidate.digest == keyDigest {
			if candidate.rawLen != uint32(len(raw)) || !bytes.Equal(s.raw[candidate.rawAt:candidate.rawAt+candidate.rawLen], raw) {
				return false
			}
			blob = i
			break
		}
	}
	newBlob := blob < 0
	if newBlob {
		if s.blobCount == stateV3StoreMaxBlobSlots || uint64(s.rawUsed)+uint64(len(raw)) > uint64(stateV3StoreRawBytes) {
			return false
		}
		blob = int(s.blobCount)
	}
	for i := 0; i < int(s.bindingCount); i++ {
		candidate := &s.bindings[i]
		if candidate.role == entry.role && candidate.commitOID == commitOID && candidate.treeOID == treeOID && candidate.pathLen == uint8(len(entry.path)) && bytes.Equal(candidate.path[:candidate.pathLen], []byte(entry.path)) && candidate.blobSlot == uint16(blob) {
			return true
		}
	}
	if s.bindingCount == stateV3StoreMaxBindings {
		return false
	}
	if newBlob {
		candidate := &s.blobs[blob]
		candidate.oid = keyOID
		candidate.digest = keyDigest
		candidate.kind = key.kind
		candidate.rawAt = s.rawUsed
		candidate.rawLen = uint32(len(raw))
		candidate.used = true
		copy(s.raw[s.rawUsed:], raw)
		s.rawUsed += uint32(len(raw))
		s.blobCount++
	}
	binding := &s.bindings[s.bindingCount]
	binding.commitOID = commitOID
	binding.treeOID = treeOID
	copy(binding.path[:], entry.path)
	binding.pathLen = uint8(len(entry.path))
	binding.role = entry.role
	binding.blobSlot = uint16(blob)
	binding.used = true
	s.bindingCount++
	return true
}

// terminationRecord returns only the exact authenticated state-record bytes
// already retained for a fixed complete snapshot. It is not a generic document
// accessor: all identity facts come from the live compact-history slot held by
// the termination-prefix issuer.
func (s *stateV3DocumentStore) terminationRecord(role stateV3AssemblyRole, snapshot stateV3CompactSnapshot, entry stateV3CompactDocumentEntry) ([]byte, bool) {
	if s == nil || role < stateV3AssemblyMinor || role > stateV3AssemblyPatch || entry.kind != stateV3DocumentRecord || entry.len == 0 {
		return nil, false
	}
	path := entry.path[:entry.len]
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	for index := 0; index < int(s.bindingCount); index++ {
		binding := s.bindings[index]
		if !binding.used || binding.role != role || binding.commitOID != snapshot.commitOID || binding.treeOID != snapshot.treeOID || binding.pathLen != entry.len || !bytes.Equal(binding.path[:binding.pathLen], path) || int(binding.blobSlot) >= int(s.blobCount) {
			continue
		}
		blob := s.blobs[binding.blobSlot]
		if !blob.used || blob.kind != stateV3DocumentRecord || blob.oid != entry.oid || blob.rawLen == 0 || uint64(blob.rawAt)+uint64(blob.rawLen) > uint64(s.rawUsed) {
			return nil, false
		}
		return append([]byte(nil), s.raw[blob.rawAt:blob.rawAt+blob.rawLen]...), true
	}
	return nil, false
}

func (s *stateV3DocumentStore) get(key stateV3DocumentKey) (stateV3ImmutableDocument, bool) {
	keyOID, keyOIDOK := decodeFixedOID(key.oid)
	keyDigest, keyDigestOK := decodeFixedDigest(key.digest)
	if !keyOIDOK || !keyDigestOK {
		return stateV3ImmutableDocument{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return stateV3ImmutableDocument{}, false
	}
	for i := 0; i < int(s.blobCount); i++ {
		blob := s.blobs[i]
		if blob.kind == key.kind && blob.oid == keyOID && blob.digest == keyDigest {
			return stateV3ImmutableDocument{raw: append([]byte(nil), s.raw[blob.rawAt:blob.rawAt+blob.rawLen]...)}, true
		}
	}
	return stateV3ImmutableDocument{}, false
}

// reset removes all transient retained documents after a failed admission.
// It is private to the operation and preserves a live store only for the
// current assembly; close remains the terminal operation boundary.
func (s *stateV3DocumentStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	clear(s.blobs[:])
	clear(s.bindings[:])
	clear(s.raw[:])
	s.blobCount = 0
	s.bindingCount = 0
	s.rawUsed = 0
}

func (s *stateV3DocumentStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	clear(s.blobs[:])
	clear(s.bindings[:])
	clear(s.raw[:])
	s.blobCount = 0
	s.bindingCount = 0
	s.rawUsed = 0
}
