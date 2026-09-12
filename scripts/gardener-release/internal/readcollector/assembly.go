// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package readcollector

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"time"
	"unsafe"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

// stateV3SpineRoot is deliberately closed: callers cannot select a ref.
type stateV3SpineRoot uint8

const (
	stateV3MinorSpine stateV3SpineRoot = iota + 1
	stateV3PatchSpine
	stateV3CoordinationSpine
)

// stateV3Spine is transient, non-authorizing commit/tree evidence. It is an
// assembly primitive rather than an authenticated operation result: state and
// coordination documents, their cross-spine relationship, and any release
// recursion have not been reconstructed at this boundary.
type stateV3Spine struct {
	stateRef      string
	checkpointOID string
	snapshots     []gardenerrelease.StateV3StateSnapshot // newest first
}

const (
	// These limits account for evidence retained after Session.Close releases
	// transport artifacts. They are independent of the session's artifact
	// store and bound all projected regular-file and changed-path evidence.
	maxStateV3SpineEntries = 8_192
	maxStateV3SpineBytes   = 8 << 20
)

type stateV3SpineBudget struct {
	entries int
	bytes   int
}

func (b *stateV3SpineBudget) reserve(entries, bytes int) bool {
	if entries < 0 || bytes < 0 || entries > maxStateV3SpineEntries-b.entries || bytes > maxStateV3SpineBytes-b.bytes {
		return false
	}
	b.entries += entries
	b.bytes += bytes
	return true
}

// collectStateV3Spine collects a single fixed checkpoint-bounded commit spine.
// It deliberately closes its Session before returning so no opaque handles or
// retained API artifacts survive either a successful collection or a failure.
//
// This is intentionally not a public operation assembler. A complete lane plus
// coordination assembly cannot fit the accepted 4096-read collector limit at
// the still-valid maximum policy history sizes; callers must not mistake this
// independently authenticated partial primitive for cross-lane authority.
func (s *Session) collectStateV3Spine(ctx context.Context, deadline time.Time, policy gardenerrelease.StateV3Policy, root stateV3SpineRoot) (stateV3Spine, Result) {
	var lease *stateV3SpineReadLease
	defer func() { s.closeStateV3Spine(lease) }()

	if ctx == nil || deadline.IsZero() || !s.now().Before(deadline) {
		return stateV3Spine{}, failure(DiagnosticDeadline)
	}
	ref, checkpoint, maximum, ok := stateV3SpinePolicy(policy, root)
	if !ok || gardenerrelease.ValidateStateV3Policy(policy) != nil {
		return stateV3Spine{}, failure(DiagnosticProtocol)
	}
	// Every node uses exactly raw-Git, REST, GraphQL, and recursive-tree reads,
	// after one fixed-root ref read. Full lane plus coordination collection is
	// blocked: two 512-node spines require 2 + 4*(512+512) = 4098 reads before
	// tree-bound document blobs or claim-release recursion.
	readBudget, budgetOK := stateV3SpineReadBudget(maximum)
	if !budgetOK {
		return stateV3Spine{}, failure(DiagnosticProtocol)
	}
	reservation, reserved := s.reserveStateV3SpineReads(readBudget)
	if !reserved {
		return stateV3Spine{}, failure(DiagnosticProtocol)
	}
	lease = reservation

	rootHandle, result := s.readStateV3SpineRoot(lease, ctx, deadline, root)
	if result.Diagnostic != DiagnosticOK {
		return stateV3Spine{}, result
	}
	current, result := s.readRawCommitForRefWithSpineLease(lease, ctx, rootHandle, deadline)
	if result.Diagnostic != DiagnosticOK {
		return stateV3Spine{}, result
	}

	budget := stateV3SpineBudget{}
	// The returned snapshots backing array is retained after Session.Close.
	// Reserve its exact capacity before allocating it; no map is needed for
	// duplicate detection because the bounded spine itself is the seen set.
	if !reserveStateV3Snapshots(&budget, maximum) || !budget.reserve(0, stateV3StringsBytes(ref, checkpoint)) {
		return stateV3Spine{}, failure(DiagnosticProtocol)
	}
	spine := stateV3Spine{
		stateRef:      strings.Clone(ref),
		checkpointOID: strings.Clone(checkpoint),
		snapshots:     make([]gardenerrelease.StateV3StateSnapshot, 0, maximum),
	}
	for len(spine.snapshots) < maximum {
		raw, rawOK := s.rawCommitFor(current)
		if !rawOK {
			return stateV3Spine{}, failure(DiagnosticProtocol)
		}
		for _, prior := range spine.snapshots {
			if prior.Commit.OID == raw.SHA {
				s.Release(current)
				return stateV3Spine{}, failure(DiagnosticConflict)
			}
		}

		restHandle, restResult := s.readRESTCommitForRawCommitWithSpineLease(lease, ctx, current, deadline)
		if restResult.Diagnostic != DiagnosticOK {
			s.Release(current)
			return stateV3Spine{}, restResult
		}
		rest, restOK := s.restCommitFor(restHandle)
		s.Release(restHandle)
		if !restOK {
			s.Release(current)
			return stateV3Spine{}, failure(DiagnosticProtocol)
		}

		graphQLHandle, graphQLResult := s.readGraphQLCommitForRawCommitWithSpineLease(lease, ctx, current, deadline)
		if graphQLResult.Diagnostic != DiagnosticOK {
			s.Release(current)
			return stateV3Spine{}, graphQLResult
		}
		graphQL, graphQLOK := s.graphQLCommitFor(graphQLHandle)
		s.Release(graphQLHandle)
		if !graphQLOK {
			s.Release(current)
			return stateV3Spine{}, failure(DiagnosticProtocol)
		}

		treeHandle, treeResult := s.readTreeForRawCommitWithSpineLease(lease, ctx, current, deadline)
		if treeResult.Diagnostic != DiagnosticOK {
			s.Release(current)
			return stateV3Spine{}, treeResult
		}
		tree, treeOK := s.treeFor(treeHandle)
		s.Release(treeHandle)
		if !treeOK {
			s.Release(current)
			return stateV3Spine{}, failure(DiagnosticProtocol)
		}
		snapshot, evidenceOK := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, raw.SHA == checkpoint, &budget)
		if !evidenceOK {
			s.Release(current)
			return stateV3Spine{}, failure(DiagnosticConflict)
		}
		spine.snapshots = append(spine.snapshots, snapshot)

		if raw.SHA == checkpoint {
			s.Release(current)
			return stateV3SpineChanges(spine, &budget)
		}
		if len(raw.Parents) != 1 {
			s.Release(current)
			return stateV3Spine{}, failure(DiagnosticRequiredEvidenceAbsent)
		}
		parent, parentResult := s.readRawCommitParentWithSpineLease(lease, ctx, current, deadline)
		s.Release(current)
		if parentResult.Diagnostic != DiagnosticOK {
			return stateV3Spine{}, parentResult
		}
		current = parent
	}
	s.Release(current)
	return stateV3Spine{}, failure(DiagnosticRequiredEvidenceAbsent)
}

func stateV3SpineReadBudget(maximum int) (int, bool) {
	if maximum <= 0 || maximum > (maxReads-1)/4 {
		return 0, false
	}
	return 1 + 4*maximum, true
}

// reserveStateV3SpineReads makes the spine's complete read budget unavailable
// to every ordinary collector path before root dispatch. The returned private
// operation capability is accepted only by private spine read methods; context
// values cannot manufacture authority to settle this reservation.
func (s *Session) reserveStateV3SpineReads(reads int) (*stateV3SpineReadLease, bool) {
	if reads <= 0 {
		return nil, false
	}
	lease := &stateV3SpineReadLease{remaining: reads}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.spineLease != nil || reads > maxReads-s.reads-s.reservedReads {
		return nil, false
	}
	s.reservedReads += reads
	s.spineLease = lease
	return lease, true
}

// closeStateV3Spine is Close scoped to one exclusive spine lease. A rejected
// concurrent collection must not close the session while the winning lease is
// still issuing its reserved reads.
func (s *Session) closeStateV3Spine(lease *stateV3SpineReadLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spineLease != nil && s.spineLease != lease {
		return
	}
	clear(s.artifacts)
	s.reservedReads = 0
	s.spineLease = nil
	s.retainedBytes = 0
	s.closed = true
}

func (s *Session) readStateV3SpineRoot(lease *stateV3SpineReadLease, ctx context.Context, deadline time.Time, root stateV3SpineRoot) (Handle, Result) {
	switch root {
	case stateV3MinorSpine:
		return s.readFixedRefWithSpineLease(lease, ctx, deadline, gardenerrelease.StateV3MinorStateRef)
	case stateV3PatchSpine:
		return s.readFixedRefWithSpineLease(lease, ctx, deadline, gardenerrelease.StateV3PatchStateRef)
	case stateV3CoordinationSpine:
		return s.readFixedRefWithSpineLease(lease, ctx, deadline, gardenerrelease.StateV3CoordinationRef)
	default:
		return Handle{}, failure(DiagnosticProtocol)
	}
}

func stateV3SpinePolicy(policy gardenerrelease.StateV3Policy, root stateV3SpineRoot) (string, string, int, bool) {
	switch root {
	case stateV3MinorSpine:
		lane := policy.StateLanes.Minor
		return lane.StateRef, lane.CheckpointOID, lane.MaxHistoryCommits, true
	case stateV3PatchSpine:
		lane := policy.StateLanes.Patch
		return lane.StateRef, lane.CheckpointOID, lane.MaxHistoryCommits, true
	case stateV3CoordinationSpine:
		coordination := policy.Coordination
		return coordination.StateRef, coordination.CheckpointOID, coordination.MaxHistoryCommits, true
	default:
		return "", "", 0, false
	}
}

func stateV3SpineChanges(spine stateV3Spine, budget *stateV3SpineBudget) (stateV3Spine, Result) {
	for index := 0; index+1 < len(spine.snapshots); index++ {
		changes, err := gardenerrelease.DeriveStateV3TreeChanges(spine.snapshots[index+1].Tree, spine.snapshots[index].Tree)
		if err != nil || !reserveStateV3ChangedPaths(budget, changes) {
			return stateV3Spine{}, failure(DiagnosticResponseInvalid)
		}
		// The parent helper controls its allocation strategy. Retain only an
		// exact-capacity copy after charging its backing storage.
		retained := make([]gardenerrelease.StateV3ChangedPath, len(changes))
		for index, change := range changes {
			retained[index] = gardenerrelease.StateV3ChangedPath{
				Path:      strings.Clone(change.Path),
				ParentOID: strings.Clone(change.ParentOID),
				ChildOID:  strings.Clone(change.ChildOID),
			}
		}
		spine.snapshots[index].Commit.ChangedPaths = retained
	}
	return spine, Result{}
}

func stateV3SnapshotEvidence(raw wireRawCommit, rest wireRESTCommit, graphQL wireGQLCommit, tree wireTree, policy gardenerrelease.StateV3Policy, checkpoint bool, budget *stateV3SpineBudget) (gardenerrelease.StateV3StateSnapshot, bool) {
	if raw.SHA != rest.SHA || raw.Tree.SHA != rest.Commit.Tree.SHA || raw.Message != rest.Commit.Message || raw.Author != rest.Commit.Author || raw.Committer != rest.Commit.Committer || !sameParents(raw.Parents, rest.Parents) {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	object := graphQL.Data.Repository.Object
	if raw.SHA != object.OID || raw.Author.Name != object.Author.Name || raw.Author.Email != object.Author.Email || raw.Author.Date != object.Author.Date || raw.Committer.Name != object.Committer.Name || raw.Committer.Email != object.Committer.Email || raw.Committer.Date != object.Committer.Date {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	roles, rolesOK := stateV3CommitRoles(raw, rest, graphQL)
	if !rolesOK || !reflect.DeepEqual(roles, policy.CommitRoles) {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	if rest.Commit.Verification.Verified == nil || !*rest.Commit.Verification.Verified ||
		rest.Commit.Verification.Reason != gardenerrelease.StateV3RequiredRESTVerificationReason ||
		object.Signature.IsValid == nil || !*object.Signature.IsValid ||
		object.Signature.WasSignedByGitHub == nil || !*object.Signature.WasSignedByGitHub ||
		object.Signature.State != gardenerrelease.StateV3RequiredSignatureState {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	if tree.SHA != raw.Tree.SHA {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	if len(raw.Parents) > 1 || (!checkpoint && len(raw.Parents) != 1) {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	parentOID := ""
	if !checkpoint {
		parentOID = raw.Parents[0].SHA
	}

	// Validate and charge every retained regular leaf before allocating the
	// projection. Directory entries are deliberately not retained and cannot
	// inflate the returned slice capacity.
	entries := 0
	entryBytes := 0
	for _, entry := range tree.Tree {
		if entry.Type == "tree" {
			continue
		}
		if entry.Type != "blob" || entry.Mode != "100644" || entry.Size == nil {
			return gardenerrelease.StateV3StateSnapshot{}, false
		}
		bytes := int(unsafe.Sizeof(gardenerrelease.StateV3StateTreeEntry{})) + stateV3StringsBytes(entry.Path, entry.Mode, entry.Type, entry.SHA)
		if bytes < 0 || entryBytes > maxStateV3SpineBytes-bytes || entries == maxStateV3SpineEntries {
			return gardenerrelease.StateV3StateSnapshot{}, false
		}
		entries++
		entryBytes += bytes
	}
	baseBytes := stateV3StringsBytes(raw.SHA, raw.Tree.SHA, parentOID, tree.SHA, rest.Commit.Verification.Reason, object.Signature.State)
	roleBytes := stateV3RolesBytes(roles)
	if baseBytes > maxStateV3SpineBytes-roleBytes || baseBytes+roleBytes > maxStateV3SpineBytes-entryBytes || budget == nil || !budget.reserve(entries, baseBytes+roleBytes+entryBytes) {
		return gardenerrelease.StateV3StateSnapshot{}, false
	}
	projected := make([]gardenerrelease.StateV3StateTreeEntry, 0, entries)
	for _, entry := range tree.Tree {
		if entry.Type == "tree" {
			continue
		}
		projected = append(projected, gardenerrelease.StateV3StateTreeEntry{
			Path: strings.Clone(entry.Path), Mode: strings.Clone(entry.Mode), Type: strings.Clone(entry.Type), OID: strings.Clone(entry.SHA),
		})
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Path < projected[j].Path })
	return gardenerrelease.StateV3StateSnapshot{
		Commit: gardenerrelease.StateV3StateCommitEvidence{
			OID: strings.Clone(raw.SHA), TreeOID: strings.Clone(raw.Tree.SHA), ParentOID: strings.Clone(parentOID), RESTVerified: true,
			RESTReason: strings.Clone(rest.Commit.Verification.Reason), GraphQLSignatureValid: true,
			WasSignedByGitHub: true, SignatureState: strings.Clone(object.Signature.State), Roles: cloneStateV3CommitRoles(roles),
		},
		Tree: gardenerrelease.StateV3StateTreeEvidence{OID: strings.Clone(tree.SHA), Complete: true, Truncated: false, Entries: projected},
	}, true
}

func reserveStateV3Snapshots(budget *stateV3SpineBudget, capacity int) bool {
	if budget == nil || capacity < 0 || capacity > maxStateV3SpineBytes/int(unsafe.Sizeof(gardenerrelease.StateV3StateSnapshot{})) {
		return false
	}
	return budget.reserve(0, capacity*int(unsafe.Sizeof(gardenerrelease.StateV3StateSnapshot{})))
}

func reserveStateV3ChangedPaths(budget *stateV3SpineBudget, paths []gardenerrelease.StateV3ChangedPath) bool {
	if budget == nil {
		return false
	}
	bytes := 0
	for _, path := range paths {
		value := int(unsafe.Sizeof(gardenerrelease.StateV3ChangedPath{})) + stateV3StringsBytes(path.Path, path.ParentOID, path.ChildOID)
		if value < 0 || bytes > maxStateV3SpineBytes-value {
			return false
		}
		bytes += value
	}
	return budget.reserve(len(paths), bytes)
}

func stateV3StringsBytes(values ...string) int {
	bytes := 0
	for _, value := range values {
		if len(value) > int(^uint(0)>>1)-bytes {
			return int(^uint(0) >> 1)
		}
		bytes += len(value)
	}
	return bytes
}

func stateV3RolesBytes(roles gardenerrelease.StateV3CommitRoles) int {
	return stateV3StringsBytes(
		roles.AuthorRaw.Name, roles.AuthorRaw.Email,
		roles.AuthorREST.Login, roles.AuthorREST.DatabaseID, roles.AuthorREST.Type,
		roles.AuthorGraphQL.Login, roles.AuthorGraphQL.DatabaseID, roles.AuthorGraphQL.Type,
		roles.CommitterRaw.Name, roles.CommitterRaw.Email,
		roles.CommitterREST.Identity.Login, roles.CommitterREST.Identity.DatabaseID, roles.CommitterREST.Identity.Type,
		roles.CommitterGraphQL.Identity.Login, roles.CommitterGraphQL.Identity.DatabaseID, roles.CommitterGraphQL.Identity.Type,
		roles.SignatureSignerGraphQL.Login, roles.SignatureSignerGraphQL.DatabaseID, roles.SignatureSignerGraphQL.Type,
	)
}

func cloneStateV3CommitRoles(roles gardenerrelease.StateV3CommitRoles) gardenerrelease.StateV3CommitRoles {
	cloneIdentity := func(identity gardenerrelease.StateV3AssociatedIdentity) gardenerrelease.StateV3AssociatedIdentity {
		return gardenerrelease.StateV3AssociatedIdentity{
			Login:      strings.Clone(identity.Login),
			DatabaseID: strings.Clone(identity.DatabaseID),
			Type:       strings.Clone(identity.Type),
		}
	}
	cloneOptionalIdentity := func(identity gardenerrelease.StateV3OptionalAssociatedIdentity) gardenerrelease.StateV3OptionalAssociatedIdentity {
		return gardenerrelease.StateV3OptionalAssociatedIdentity{Present: identity.Present, Identity: cloneIdentity(identity.Identity)}
	}
	return gardenerrelease.StateV3CommitRoles{
		AuthorRaw: gardenerrelease.StateV3RawIdentity{
			Name: strings.Clone(roles.AuthorRaw.Name), Email: strings.Clone(roles.AuthorRaw.Email),
		},
		AuthorREST: cloneIdentity(roles.AuthorREST), AuthorGraphQL: cloneIdentity(roles.AuthorGraphQL),
		CommitterRaw: gardenerrelease.StateV3RawIdentity{
			Name: strings.Clone(roles.CommitterRaw.Name), Email: strings.Clone(roles.CommitterRaw.Email),
		},
		CommitterREST: cloneOptionalIdentity(roles.CommitterREST), CommitterGraphQL: cloneOptionalIdentity(roles.CommitterGraphQL),
		SignatureSignerGraphQL: cloneIdentity(roles.SignatureSignerGraphQL),
	}
}

func sameParents(raw, rest []wireParent) bool {
	if len(raw) != len(rest) {
		return false
	}
	for index := range raw {
		if raw[index].SHA != rest[index].SHA {
			return false
		}
	}
	return true
}

func stateV3CommitRoles(raw wireRawCommit, rest wireRESTCommit, graphQL wireGQLCommit) (gardenerrelease.StateV3CommitRoles, bool) {
	object := graphQL.Data.Repository.Object
	if rest.Author == nil || object.Author.User == nil || object.Signature.Signer == nil {
		return gardenerrelease.StateV3CommitRoles{}, false
	}
	roles := gardenerrelease.StateV3CommitRoles{
		AuthorRaw:              gardenerrelease.StateV3RawIdentity{Name: raw.Author.Name, Email: raw.Author.Email},
		AuthorREST:             gardenerrelease.StateV3AssociatedIdentity{Login: rest.Author.Login, DatabaseID: decimalID(rest.Author.ID), Type: rest.Author.Type},
		AuthorGraphQL:          gardenerrelease.StateV3AssociatedIdentity{Login: object.Author.User.Login, DatabaseID: decimalIDPtr(object.Author.User.DatabaseID), Type: object.Author.User.Typename},
		CommitterRaw:           gardenerrelease.StateV3RawIdentity{Name: raw.Committer.Name, Email: raw.Committer.Email},
		SignatureSignerGraphQL: gardenerrelease.StateV3AssociatedIdentity{Login: object.Signature.Signer.Login, DatabaseID: decimalIDPtr(object.Signature.Signer.DatabaseID), Type: object.Signature.Signer.Typename},
	}
	if rest.Committer != nil {
		roles.CommitterREST = gardenerrelease.StateV3OptionalAssociatedIdentity{Present: true, Identity: gardenerrelease.StateV3AssociatedIdentity{Login: rest.Committer.Login, DatabaseID: decimalID(rest.Committer.ID), Type: rest.Committer.Type}}
	}
	if object.Committer.User != nil {
		roles.CommitterGraphQL = gardenerrelease.StateV3OptionalAssociatedIdentity{Present: true, Identity: gardenerrelease.StateV3AssociatedIdentity{Login: object.Committer.User.Login, DatabaseID: decimalIDPtr(object.Committer.User.DatabaseID), Type: object.Committer.User.Typename}}
	}
	return roles, roles.AuthorGraphQL.DatabaseID != "" && roles.SignatureSignerGraphQL.DatabaseID != ""
}

func decimalID(value int64) string {
	if value <= 0 {
		return ""
	}
	return fmtInt(value)
}

func decimalIDPtr(value *int64) string {
	if value == nil {
		return ""
	}
	return decimalID(*value)
}

func fmtInt(value int64) string {
	const digits = "0123456789"
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
