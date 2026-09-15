// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package strictcollector contains the isolated, read-only v3 GitHub collector.
// It deliberately has no production constructor. A future authenticated
// snapshot assembler belongs in this package, so callers cannot obtain its
// transport, manufacture a request, or inspect mutable predecessor evidence.
package strictcollector

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

const (
	apiOrigin        = "https://api.github.com"
	apiVersion       = "2022-11-28"
	repository       = "DataDog/dd-trace-go"
	maxResponseBytes = 16 << 20
	maxArtifacts     = 128
	maxRetainedBytes = 32 << 20
	maxReads         = 4096
	maxAttempts      = 4
)

// Result is safe for callers to retain or report. It never contains raw bytes,
// response headers, a transport error, or decoded evidence.
type Result struct {
	Descriptor    string
	Attempts      int
	Status        int
	RequestSHA256 string
	BodySHA256    string
	Diagnostic    string
	responseBytes int
}

const (
	DiagnosticOK                     = ""
	DiagnosticTransport              = "github_read_transport"
	DiagnosticRetryable              = "github_read_retryable"
	DiagnosticProtocol               = "github_read_protocol"
	DiagnosticUnauthorized           = "github_read_unauthorized"
	DiagnosticRequiredEvidenceAbsent = "github_required_evidence_absent"
	DiagnosticConflict               = "github_reconciliation_conflict"
	DiagnosticDeadline               = "github_read_deadline"
	DiagnosticResponseInvalid        = "github_response_invalid"
)

// handle is opaque continuation evidence. Its fields cannot be constructed or
// changed outside this internal package. A handle is accepted only by its
// exact successor on the session which issued it.
type handle struct {
	session *session
	nonce   string
}

// session owns the only transport and mutable artifact store. There is no
// exported constructor: tests construct it inside this package, and production
// construction remains deferred until credentials and policy are reviewed.
type session struct {
	transport http.RoundTripper
	now       func() time.Time
	// ordinary is installed only by the private setup capability below.
	// Ordinary continuation calls deliberately cannot select the request
	// context or deadline that reaches the transport.
	ordinary         *ordinaryOperation
	mu               sync.Mutex
	artifacts        map[string]*artifact
	reads            int
	reservedReads    int
	spineLease       *stateV3SpineReadLease
	spineContext     context.Context
	spineDeadline    time.Time
	spineRoot        stateV3SpineRoot
	assembly         *stateV3AssemblyQuota
	assemblyLease    *stateV3AssemblyReadLease
	assemblyRole     stateV3AssemblyRole
	assemblyContext  context.Context
	assemblyDeadline time.Time
	assemblyReads    int
	assemblyBytes    int
	assemblyLive     bool
	// compactHistory is installed only while a private state-lane admission
	// pass is live. Compact blob reads derive entries from this session-owned
	// authenticated history; callers never supply a snapshot or blob OID.
	compactHistory             *stateV3CompactLaneHistory
	coordinationCompactHistory *stateV3CompactCoordinationHistory
	compactGeneration          uint64
	retainedBytes              int
	closed                     bool
}

// ordinaryOperation is immutable after installation. It is deliberately
// retained as one session-owned value so Close can detach and cancel an
// in-flight ordinary request without leaving any reusable configuration.
type ordinaryOperation struct {
	context  context.Context
	deadline time.Time
	cancel   context.CancelFunc
}

// installOrdinary is the sole ordinary-operation setup capability. There is no
// production session constructor or production caller yet; the source-level
// authority policy pins this method and rejects a production call until a
// future reviewed factory is introduced.
func (s *session) installOrdinary(parent context.Context, deadline time.Time) bool {
	if parent == nil || deadline.IsZero() || !s.now().Before(deadline) {
		return false
	}
	context, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.closed || s.ordinary != nil {
		s.mu.Unlock()
		cancel()
		return false
	}
	s.ordinary = &ordinaryOperation{context: context, deadline: deadline, cancel: cancel}
	s.mu.Unlock()
	return true
}

// stateV3SpineReadLease is an assembly-owned capability. It is deliberately
// non-zero-sized and is passed only through private spine read methods;
// ordinary session reads cannot present or settle it.
type stateV3SpineReadLease struct {
	remaining int
}

// stateV3AssemblyReadLease is issued only to an operation child. Unlike a
// context value, it is checked by identity at the collector settlement point.
type stateV3AssemblyReadLease struct {
	remaining int
}

type readMode uint8

const (
	readOrdinary readMode = iota
	readSpine
	readAssembly
)

type kind string

const (
	kindControlRef    kind = "control_ref"
	kindBranchRef     kind = "release_branch_ref"
	kindTagRef        kind = "tag_ref"
	kindRawCommit     kind = "raw_commit"
	kindRESTCommit    kind = "rest_commit"
	kindGraphQLCommit kind = "graphql_commit"
	kindTree          kind = "tree"
	kindBlob          kind = "blob"
	kindTagObject     kind = "annotated_tag"
)

type artifact struct {
	mu          sync.Mutex
	kind        kind
	used        uint8
	usedEntries map[int]bool
	bytes       int
	ref         wireRef
	raw         wireRawCommit
	rest        wireRESTCommit
	gql         wireGQLCommit
	tree        wireTree
	blob        wireBlob
	tag         wireTag
}
type refEvidence struct{ Ref, SHA, Type string }
type commitEvidence struct {
	SHA, Tree string
	Parents   []string
}
type treeEntry struct{ Path, SHA, Type, Mode string }
type treeEvidence struct {
	Entries []treeEntry
	used    map[int]struct{}
}

const (
	edgeRaw uint8 = 1 << iota
	edgeRest
	edgeGraphQL
	edgeTree
	edgeParent
	edgeTagObject
)

// ReadMinorStateRef, ReadPatchStateRef, and ReadCoordinationRef are the only
// ordinary root reads. Each creates a closed fixed request for its exact ref;
// ordinary object reads likewise derive their OIDs only after consuming an
// opaque authenticated predecessor handle.
func (s *session) ReadMinorStateRef(_ context.Context, _ time.Time) (handle, Result) {
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindControlRef, purpose: fixedRequestRoot, ref: gardenerrelease.StateV3MinorStateRef})
}
func (s *session) ReadPatchStateRef(_ context.Context, _ time.Time) (handle, Result) {
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindControlRef, purpose: fixedRequestRoot, ref: gardenerrelease.StateV3PatchStateRef})
}
func (s *session) ReadCoordinationRef(_ context.Context, _ time.Time) (handle, Result) {
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindControlRef, purpose: fixedRequestRoot, ref: gardenerrelease.StateV3CoordinationRef})
}

// ReadRawCommitForRef can read only the commit named by a live ref handle.
func (s *session) ReadRawCommitForRef(_ context.Context, prior handle, _ time.Time) (handle, Result) {
	ref, ok := s.consumeRef(prior, edgeRaw, kindControlRef, kindBranchRef)
	if !ok || ref.Type != "commit" || !validOID(ref.SHA) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindRawCommit, purpose: fixedRequestRaw, oid: ref.SHA})
}
func (s *session) ReadRawCommitParent(_ context.Context, prior handle, _ time.Time) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeParent)
	if !ok || len(commit.Parents) != 1 || !validOID(commit.Parents[0]) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindRawCommit, purpose: fixedRequestRaw, oid: commit.Parents[0]})
}
func (s *session) ReadRESTCommitForRawCommit(_ context.Context, prior handle, _ time.Time) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeRest)
	if !ok || !validOID(commit.SHA) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindRESTCommit, purpose: fixedRequestREST, oid: commit.SHA})
}
func (s *session) ReadGraphQLCommitForRawCommit(_ context.Context, prior handle, _ time.Time) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeGraphQL)
	if !ok || !validOID(commit.SHA) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindGraphQLCommit, purpose: fixedRequestGraphQL, oid: commit.SHA})
}
func (s *session) ReadTreeForRawCommit(_ context.Context, prior handle, _ time.Time) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeTree)
	if !ok || !validOID(commit.Tree) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindTree, purpose: fixedRequestTree, oid: commit.Tree})
}
func (s *session) ReadBlobForTreeEntry(_ context.Context, prior handle, index int, _ time.Time) (handle, Result) {
	entry, ok := s.consumeTreeEntry(prior, index)
	if !ok || !validOID(entry.SHA) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedOrdinary, kind: kindBlob, purpose: fixedRequestBlob, oid: entry.SHA})
}

// Close deterministically releases every transient evidence artifact retained
// by a partial collection. A closed session has no retained evidence.
func (s *session) Close() {
	s.mu.Lock()
	ordinary := s.ordinary
	s.ordinary = nil
	clear(s.artifacts)
	s.reservedReads = 0
	s.spineLease = nil
	s.spineContext = nil
	s.spineDeadline = time.Time{}
	s.spineRoot = 0
	s.assembly = nil
	s.assemblyLease = nil
	s.assemblyRole = 0
	s.assemblyContext = nil
	s.assemblyDeadline = time.Time{}
	s.assemblyReads = 0
	s.assemblyBytes = 0
	s.assemblyLive = false
	s.compactHistory = nil
	s.coordinationCompactHistory = nil
	s.compactGeneration++
	s.retainedBytes = 0
	s.closed = true
	s.mu.Unlock()
	if ordinary != nil {
		ordinary.cancel()
	}
}

// Release makes an issued handle unavailable to all later continuations.
func (s *session) Release(handle handle) {
	if handle.session != s || handle.nonce == "" {
		return
	}
	s.mu.Lock()
	if artifact := s.artifacts[handle.nonce]; artifact != nil {
		s.retainedBytes -= artifact.bytes
		delete(s.artifacts, handle.nonce)
	}
	s.mu.Unlock()
}
func (s *session) consumeRef(handle handle, edge uint8, allowed ...kind) (refEvidence, bool) {
	a := s.get(handle)
	if a == nil {
		return refEvidence{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ok := false
	for _, k := range allowed {
		ok = ok || a.kind == k
	}
	if !ok || a.used&edge != 0 || !s.retained(handle, a) {
		return refEvidence{}, false
	}
	a.used |= edge
	v := refEvidence{Ref: a.ref.Ref, SHA: a.ref.Object.SHA, Type: a.ref.Object.Type}
	s.Release(handle)
	return v, true
}
func (s *session) consumeCommit(handle handle, edge uint8) (commitEvidence, bool) {
	a := s.get(handle)
	if a == nil {
		return commitEvidence{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kind != kindRawCommit || a.used&edge != 0 || !s.retained(handle, a) {
		return commitEvidence{}, false
	}
	a.used |= edge
	v := commitEvidence{SHA: a.raw.SHA, Tree: a.raw.Tree.SHA}
	for _, p := range a.raw.Parents {
		v.Parents = append(v.Parents, p.SHA)
	}
	return v, true
}
func (s *session) consumeTreeEntry(handle handle, index int) (treeEntry, bool) {
	a := s.get(handle)
	if a == nil {
		return treeEntry{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kind != kindTree || index < 0 || index >= len(a.tree.Tree) || a.usedEntries[index] || !s.retained(handle, a) {
		return treeEntry{}, false
	}
	if a.tree.Tree[index].Type != "blob" || a.tree.Tree[index].Mode != "100644" {
		return treeEntry{}, false
	}
	a.usedEntries[index] = true
	e := a.tree.Tree[index]
	return treeEntry{e.Path, e.SHA, e.Type, e.Mode}, true
}
func (s *session) get(handle handle) *artifact {
	if handle.session != s || handle.nonce == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.artifacts[handle.nonce]
}

func (s *session) retained(handle handle, artifact *artifact) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.artifacts[handle.nonce] == artifact
}

// The following accessors are intentionally package-private. A future
// assembler belongs beside session and receives defensive typed copies only;
// raw HTTP bytes and a generic artifact accessor never cross this boundary.
func (s *session) rawCommitFor(handle handle) (wireRawCommit, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindRawCommit {
		return wireRawCommit{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireRawCommit{}, false
	}
	return cloneRawCommit(a.raw), true
}
func (s *session) restCommitFor(handle handle) (wireRESTCommit, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindRESTCommit {
		return wireRESTCommit{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireRESTCommit{}, false
	}
	return cloneRESTCommit(a.rest), true
}
func (s *session) graphQLCommitFor(handle handle) (wireGQLCommit, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindGraphQLCommit {
		return wireGQLCommit{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireGQLCommit{}, false
	}
	return cloneGQLCommit(a.gql), true
}
func (s *session) treeFor(handle handle) (wireTree, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindTree {
		return wireTree{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireTree{}, false
	}
	return cloneTree(a.tree), true
}
func (s *session) blobFor(handle handle) (wireBlob, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindBlob {
		return wireBlob{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireBlob{}, false
	}
	return cloneBlob(a.blob), true
}

func (s *session) tagFor(handle handle) (wireTag, bool) {
	a := s.get(handle)
	if a == nil || a.kind != kindTagObject {
		return wireTag{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !s.retained(handle, a) {
		return wireTag{}, false
	}
	return cloneTag(a.tag), true
}

// fixedRequest is a closed, purpose-specific read description. It is issued
// only by fixed-root and opaque-handle continuation methods below; settlement
// derives its context, deadline, and reservation mode from the active session.
type fixedRequest struct {
	scope   fixedRequestScope
	kind    kind
	purpose fixedRequestPurpose
	oid     string
	ref     string
}

type fixedRequestScope uint8

const (
	fixedOrdinary fixedRequestScope = iota + 1
	fixedAssembly
	fixedSpine
)

type fixedRequestPurpose uint8

const (
	fixedRequestRoot fixedRequestPurpose = iota + 1
	fixedRequestRaw
	fixedRequestREST
	fixedRequestGraphQL
	fixedRequestTree
	fixedRequestBlob
)

// settleFixed is the only active-operation settlement point. It accepts the
// closed request value rather than caller-selected HTTP or decode inputs, and
// derives its context, deadline, and reservation mode from active session state.
func (s *session) settleFixed(request fixedRequest) (handle, Result) {
	var mode readMode
	s.mu.Lock()
	var ctx context.Context
	var deadline time.Time
	switch request.scope {
	case fixedOrdinary:
		if s.ordinary == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
		ctx, deadline, mode = s.ordinary.context, s.ordinary.deadline, readOrdinary
		if ctx == nil || deadline.IsZero() || !s.now().Before(deadline) {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	case fixedAssembly:
		ctx, deadline, mode = s.assemblyContext, s.assemblyDeadline, readAssembly
		if !s.assemblyLive || ctx == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	case fixedSpine:
		ctx, deadline, mode = s.spineContext, s.spineDeadline, readSpine
		if s.spineLease == nil || ctx == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	default:
		s.mu.Unlock()
		return handle{}, failure(DiagnosticProtocol)
	}
	s.mu.Unlock()
	var method, path string
	var body []byte
	var decode func([]byte) (*artifact, bool)
	switch request.purpose {
	case fixedRequestRoot:
		if request.kind != kindControlRef || request.ref == "" {
			return handle{}, failure(DiagnosticProtocol)
		}
		method = http.MethodGet
		path = "/repos/" + repository + "/git/ref/" + strings.TrimPrefix(request.ref, "refs/")
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRef(raw, request.ref, "commit")
			return &artifact{kind: kindControlRef, ref: value}, ok
		}
	case fixedRequestRaw:
		if request.kind != kindRawCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/commits/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRawCommit(raw, request.oid)
			return &artifact{kind: kindRawCommit, raw: value}, ok
		}
	case fixedRequestREST:
		if request.kind != kindRESTCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/commits/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRESTCommit(raw, request.oid)
			return &artifact{kind: kindRESTCommit, rest: value}, ok
		}
	case fixedRequestGraphQL:
		if request.kind != kindGraphQLCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path, body = http.MethodPost, "/graphql", stateV3GraphQLBody(request.oid)
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeGQLCommit(raw, request.oid)
			return &artifact{kind: kindGraphQLCommit, gql: value}, ok
		}
	case fixedRequestTree:
		if request.kind != kindTree || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/trees/"+request.oid+"?recursive=1"
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeTree(raw, request.oid)
			return &artifact{kind: kindTree, tree: value}, ok
		}
	case fixedRequestBlob:
		if request.kind != kindBlob || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/blobs/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeBlob(raw, request.oid, maxResponseBytes)
			return &artifact{kind: kindBlob, blob: value}, ok
		}
	default:
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.executeRequest(mode, ctx, deadline, request.kind, method, path, body, decode)
}

func (s *session) reserveAssembly(role stateV3AssemblyRole, reads, bytes int, quota *stateV3AssemblyQuota) (*stateV3AssemblyReadLease, bool) {
	if role < stateV3AssemblyMinor || role > stateV3AssemblyCoordination || reads <= 0 || bytes <= 0 || quota == nil {
		return nil, false
	}
	lease := &stateV3AssemblyReadLease{remaining: reads}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.spineLease != nil || s.assembly != nil || reads > maxReads-s.reads-s.reservedReads {
		return nil, false
	}
	s.reservedReads += reads
	s.assemblyReads = reads
	s.assemblyBytes = bytes
	s.assembly = quota
	s.assemblyLease = lease
	s.assemblyRole = role
	return lease, true
}

func (s *session) releaseAssembly() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assembly == nil {
		return
	}
	s.reservedReads -= s.assemblyReads
	s.assemblyReads = 0
	s.assemblyBytes = 0
	s.assembly = nil
	s.assemblyLease = nil
	s.assemblyRole = 0
	s.assemblyContext = nil
	s.assemblyDeadline = time.Time{}
	s.assemblyLive = false
}

func (s *session) activateAssembly(ctx context.Context, deadline time.Time, role stateV3AssemblyRole) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.assembly == nil || s.assemblyLive || ctx == nil || deadline.IsZero() || !s.now().Before(deadline) || role != s.assemblyRole {
		return false
	}
	s.assemblyContext = ctx
	s.assemblyDeadline = deadline
	s.assemblyLive = true
	return true
}

// readAssemblyRoot and all assembly continuations derive route, identity,
// context, deadline, and quota from active session state and opaque handles.
func (s *session) readAssemblyRoot() (handle, Result) {
	s.mu.Lock()
	role, live := s.assemblyRole, s.assemblyLive
	s.mu.Unlock()
	if !live {
		return handle{}, failure(DiagnosticProtocol)
	}
	var ref string
	switch role {
	case stateV3AssemblyMinor:
		ref = gardenerrelease.StateV3MinorStateRef
	case stateV3AssemblyPatch:
		ref = gardenerrelease.StateV3PatchStateRef
	case stateV3AssemblyCoordination:
		ref = gardenerrelease.StateV3CoordinationRef
	default:
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindControlRef, purpose: fixedRequestRoot, ref: ref})
}
func (s *session) readAssemblyRawForRef(prior handle) (handle, Result) {
	ref, ok := s.consumeRef(prior, edgeRaw, kindControlRef)
	if !ok || ref.Type != "commit" {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindRawCommit, purpose: fixedRequestRaw, oid: ref.SHA})
}
func (s *session) readAssemblyRawParent(prior handle) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeParent)
	if !ok || len(commit.Parents) != 1 {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindRawCommit, purpose: fixedRequestRaw, oid: commit.Parents[0]})
}
func (s *session) readAssemblyREST(prior handle) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeRest)
	if !ok {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindRESTCommit, purpose: fixedRequestREST, oid: commit.SHA})
}
func (s *session) readAssemblyGraphQL(prior handle) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeGraphQL)
	if !ok {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindGraphQLCommit, purpose: fixedRequestGraphQL, oid: commit.SHA})
}
func (s *session) readAssemblyTree(prior handle) (handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeTree)
	if !ok || !validOID(commit.Tree) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindTree, purpose: fixedRequestTree, oid: commit.Tree})
}

// readAssemblyBlob consumes only an approved regular tree entry. Its OID is
// derived from the retained tree, never supplied by an assembly caller.
func (s *session) readAssemblyBlob(prior handle, index int) (handle, Result) {
	entry, ok := s.consumeTreeEntry(prior, index)
	if !ok || entry.Type != "blob" || entry.Mode != "100644" || !validOID(entry.SHA) {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: fixedRequestBlob, oid: entry.SHA})
}

func stateV3GraphQLBody(oid string) []byte {
	return []byte(`{"query":"query StateV3Commit($owner:String!,$name:String!,$oid:GitObjectID!){repository(owner:$owner,name:$name){object(oid:$oid){__typename ... on Commit{oid author{name email date user{__typename login databaseId}} committer{name email date user{__typename login databaseId}} signature{isValid state wasSignedByGitHub signer{__typename login databaseId}}}}}}","variables":{"owner":"DataDog","name":"dd-trace-go","oid":"` + oid + `"}}`)
}

func (s *session) closeAssembly() {
	s.Close()
}

// executeRequest is the sole settlement point. Its private mode is selected
// only by closed spine or assembly continuation methods; ordinary reads cannot
// spend an active reservation.
func (s *session) executeRequest(mode readMode, ctx context.Context, deadline time.Time, k kind, method, path string, body []byte, decode func([]byte) (*artifact, bool)) (handle, Result) {
	if deadline.IsZero() || !s.now().Before(deadline) || ctx == nil {
		return handle{}, failure(DiagnosticDeadline)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return handle{}, failure(DiagnosticProtocol)
	}
	if s.assembly != nil {
		if mode != readAssembly || !s.assemblyLive || s.assemblyLease == nil || s.assemblyLease.remaining == 0 || s.assemblyReads == 0 || s.reservedReads == 0 {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
		s.assemblyLease.remaining--
		s.assemblyReads--
		s.reservedReads--
		s.reads++
	} else if s.spineLease != nil {
		if mode != readSpine || s.spineLease.remaining == 0 || s.reservedReads == 0 {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
		s.spineLease.remaining--
		s.reservedReads--
		s.reads++
	} else {
		if mode != readOrdinary || s.reads >= maxReads {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
		s.reads++
	}
	s.mu.Unlock()
	requestContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !s.chargeAssemblyAttempt() {
			return handle{}, failure(DiagnosticProtocol)
		}
		obs, retry, delay, result := s.readOnce(requestContext, k, method, path, body, attempt)
		if !s.chargeAssemblyBytes(result.responseBytes) {
			return handle{}, failure(DiagnosticProtocol)
		}
		if result.Diagnostic == DiagnosticOK {
			if artifact, ok := decode(obs); ok {
				// Raw bytes remain confined to this stack frame. Their bounded
				// source size is charged to the decoded artifact budget.
				artifact.bytes = len(obs) + len(artifact.blob.Raw)
				return s.issue(artifact, result)
			}
			result.Diagnostic = DiagnosticResponseInvalid
			return handle{}, result
		}
		if !retry || attempt == maxAttempts {
			return handle{}, result
		}
		if delay <= 0 || s.now().Add(delay).After(deadline) || !sleep(requestContext, delay) {
			result.Diagnostic = DiagnosticDeadline
			return handle{}, result
		}
	}
	return handle{}, failure(DiagnosticProtocol)
}
func (s *session) chargeAssemblyAttempt() bool {
	s.mu.Lock()
	quota := s.assembly
	live := s.assemblyLive
	s.mu.Unlock()
	return quota == nil || !live || quota.chargeAttempt()
}

func (s *session) chargeAssemblyBytes(bytes int) bool {
	s.mu.Lock()
	quota := s.assembly
	live := s.assemblyLive
	if live && bytes > s.assemblyBytes {
		s.mu.Unlock()
		return false
	}
	if live {
		s.assemblyBytes -= bytes
	}
	s.mu.Unlock()
	return quota == nil || !live || quota.chargeBytes(bytes)
}

func (s *session) readOnce(ctx context.Context, k kind, method, path string, body []byte, attempt int) ([]byte, bool, time.Duration, Result) {
	result := Result{Descriptor: string(k), Attempts: attempt, RequestSHA256: digest(body)}
	req, err := http.NewRequestWithContext(ctx, method, apiOrigin+path, bytes.NewReader(body))
	if err != nil {
		result.Diagnostic = DiagnosticProtocol
		return nil, false, 0, result
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := s.transport.RoundTrip(req)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil || response == nil || response.Body == nil {
		result.Diagnostic = deadlineOr(ctx, DiagnosticTransport)
		return nil, result.Diagnostic == DiagnosticTransport, time.Second, result
	}
	result.Status = response.StatusCode
	if ctx.Err() != nil {
		result.Diagnostic = DiagnosticDeadline
		return nil, false, 0, result
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		result.Diagnostic = DiagnosticProtocol
		return nil, false, 0, result
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	result.responseBytes = len(raw)
	if ctx.Err() != nil {
		result.Diagnostic = DiagnosticDeadline
		return nil, false, 0, result
	}
	if err != nil {
		result.Diagnostic = DiagnosticTransport
		return nil, true, time.Second, result
	}
	if len(raw) > maxResponseBytes {
		result.Diagnostic = DiagnosticProtocol
		return nil, false, 0, result
	}
	result.BodySHA256 = digest(raw)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if len(raw) == 0 {
			result.Diagnostic = DiagnosticProtocol
			return nil, false, 0, result
		}
		return raw, false, 0, result
	}
	if response.StatusCode == 429 {
		delay, ok := retryAfter(response.Header.Get("Retry-After"))
		if !ok {
			result.Diagnostic = DiagnosticProtocol
			return nil, false, 0, result
		}
		result.Diagnostic = DiagnosticRetryable
		return nil, true, delay, result
	}
	if response.StatusCode == 403 && response.Header.Get("X-RateLimit-Remaining") == "0" {
		delay, ok := rateLimitDelay(response.Header.Get("X-RateLimit-Reset"), s.now())
		if !ok {
			result.Diagnostic = DiagnosticProtocol
			return nil, false, 0, result
		}
		result.Diagnostic = DiagnosticRetryable
		return nil, true, delay, result
	}
	if response.StatusCode >= 500 {
		result.Diagnostic = DiagnosticRetryable
		return nil, true, time.Second, result
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		result.Diagnostic = DiagnosticUnauthorized
	} else if response.StatusCode == 404 {
		result.Diagnostic = DiagnosticRequiredEvidenceAbsent
	} else if response.StatusCode == 409 || response.StatusCode == 422 {
		result.Diagnostic = DiagnosticConflict
	} else {
		result.Diagnostic = DiagnosticProtocol
	}
	return nil, false, 0, result
}
func retryAfter(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 || seconds > int64((24*time.Hour)/time.Second) {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
func rateLimitDelay(value string, now time.Time) (time.Duration, bool) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	delay := time.Unix(seconds, 0).Sub(now)
	return delay, delay > 0 && delay <= 24*time.Hour
}
func (s *session) issue(decoded *artifact, r Result) (handle, Result) {
	if decoded == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	bytes := artifactBytes(decoded)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return handle{}, failure(DiagnosticTransport)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.artifacts) >= maxArtifacts || bytes < 0 || bytes > maxRetainedBytes-s.retainedBytes {
		return handle{}, failure(DiagnosticProtocol)
	}
	a := cloneArtifact(decoded)
	a.bytes = bytes
	a.usedEntries = make(map[int]bool)
	n := hex.EncodeToString(nonce[:])
	s.artifacts[n] = a
	s.retainedBytes += a.bytes
	return handle{session: s, nonce: n}, r
}

// cloneArtifact transfers only immutable evidence into session ownership. It
// deliberately does not copy locks or consumption state from a decoder-local
// value, and all pointer/slice-backed values receive fresh backing storage.
func cloneArtifact(value *artifact) *artifact {
	return &artifact{
		kind: value.kind,
		ref:  value.ref,
		raw:  cloneRawCommit(value.raw),
		rest: cloneRESTCommit(value.rest),
		gql:  cloneGQLCommit(value.gql),
		tree: cloneTree(value.tree),
		blob: cloneBlob(value.blob),
		tag:  cloneTag(value.tag),
	}
}

// artifactBytes charges all allocations retained by an artifact. It counts
// the artifact and slice backing arrays, each separately allocated pointer
// target, every string/byte backing store, and the per-tree-entry consumption
// map reservation. The decoder's raw response is intentionally not charged:
// it is discarded before retention.
func artifactBytes(a *artifact) int {
	total := int(unsafe.Sizeof(*a))
	total += wireRefBytes(a.ref)
	total += wireRawCommitBytes(a.raw)
	total += wireRESTCommitBytes(a.rest)
	total += wireGQLCommitBytes(a.gql)
	total += wireTreeBytes(a.tree)
	total += wireBlobBytes(a.blob)
	total += wireTagBytes(a.tag)
	// map[int]bool has an implementation-dependent bucket layout. Reserve a
	// full cache line per possible entry, conservatively covering its buckets
	// and load slack without charging response bytes that are not retained.
	total += len(a.tree.Tree) * 64
	return total
}
func stringBytes(values ...string) int {
	total := 0
	for _, value := range values {
		total += len(value)
	}
	return total
}
func boolPtrBytes(value *bool) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value))
}
func intPtrBytes(value *int) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value))
}
func int64PtrBytes(value *int64) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value))
}
func stringPtrBytes(value *string) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value)) + len(*value)
}
func verificationBytes(value wireVerification) int {
	return stringBytes(value.Reason) + boolPtrBytes(value.Verified) + stringPtrBytes(value.Signature) + stringPtrBytes(value.Payload) + stringPtrBytes(value.VerifiedAt)
}
func identityBytes(value wireIdentity) int { return stringBytes(value.Name, value.Email, value.Date) }
func parentBytes(value wireParent) int     { return stringBytes(value.SHA, value.URL, value.HTMLURL) }
func treeRefBytes(value wireTreeRef) int   { return stringBytes(value.SHA, value.URL) }
func wireRefBytes(value wireRef) int {
	return stringBytes(value.Ref, value.NodeID, value.URL, value.Object.SHA, value.Object.Type, value.Object.URL)
}
func wireRawCommitBytes(value wireRawCommit) int {
	total := stringBytes(value.SHA, value.NodeID, value.URL, value.HTMLURL, value.Message) + identityBytes(value.Author) + identityBytes(value.Committer) + treeRefBytes(value.Tree) + verificationBytes(value.Verification) + cap(value.Parents)*int(unsafe.Sizeof(wireParent{}))
	for _, parent := range value.Parents {
		total += parentBytes(parent)
	}
	return total
}
func restIdentityBytes(value *wireRESTIdentity) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value)) + stringBytes(value.Login, value.NodeID, value.AvatarURL, value.GravatarID, value.HTMLURL, value.URL, value.FollowersURL, value.FollowingURL, value.GistsURL, value.StarredURL, value.SubscriptionsURL, value.OrganizationsURL, value.ReposURL, value.EventsURL, value.ReceivedEventsURL, value.Type, value.UserViewType) + boolPtrBytes(value.SiteAdmin)
}
func wireRESTCommitBytes(value wireRESTCommit) int {
	total := stringBytes(value.SHA, value.NodeID, value.URL, value.HTMLURL, value.CommentsURL, value.Commit.Message, value.Commit.URL) + identityBytes(value.Commit.Author) + identityBytes(value.Commit.Committer) + treeRefBytes(value.Commit.Tree) + intPtrBytes(value.Commit.CommentCount) + verificationBytes(value.Commit.Verification) + restIdentityBytes(value.Author) + restIdentityBytes(value.Committer) + intPtrBytes(value.Stats.Total) + intPtrBytes(value.Stats.Additions) + intPtrBytes(value.Stats.Deletions) + cap(value.Parents)*int(unsafe.Sizeof(wireParent{}))
	for _, parent := range value.Parents {
		total += parentBytes(parent)
	}
	total += cap(value.Files) * int(unsafe.Sizeof(wireRESTFile{}))
	for _, file := range value.Files {
		total += stringBytes(file.SHA, file.Filename, file.Status, file.BlobURL, file.RawURL, file.ContentsURL) + intPtrBytes(file.Additions) + intPtrBytes(file.Deletions) + intPtrBytes(file.Changes) + stringPtrBytes(file.Patch) + stringPtrBytes(file.PreviousFilename)
	}
	return total
}
func gqlIdentityBytes(value *wireGQLIdentity) int {
	if value == nil {
		return 0
	}
	return int(unsafe.Sizeof(*value)) + stringBytes(value.Typename, value.Login) + int64PtrBytes(value.DatabaseID)
}
func wireGQLCommitBytes(value wireGQLCommit) int {
	object := value.Data.Repository.Object
	return stringBytes(object.Typename, object.OID, object.Author.Name, object.Author.Email, object.Author.Date, object.Committer.Name, object.Committer.Email, object.Committer.Date, object.Signature.State) + gqlIdentityBytes(object.Author.User) + gqlIdentityBytes(object.Committer.User) + boolPtrBytes(object.Signature.IsValid) + boolPtrBytes(object.Signature.WasSignedByGitHub) + gqlIdentityBytes(object.Signature.Signer)
}
func wireTreeBytes(value wireTree) int {
	total := stringBytes(value.SHA, value.URL) + boolPtrBytes(value.Truncated) + cap(value.Tree)*int(unsafe.Sizeof(wireTreeEntry{}))
	for _, entry := range value.Tree {
		total += stringBytes(entry.Path, entry.Mode, entry.Type, entry.SHA, entry.URL) + int64PtrBytes(entry.Size)
	}
	return total
}
func wireBlobBytes(value wireBlob) int {
	return stringBytes(value.SHA, value.NodeID, value.URL, value.Content, value.Encoding) + int64PtrBytes(value.Size) + cap(value.Raw)
}
func wireTagBytes(value wireTag) int {
	return stringBytes(value.NodeID, value.SHA, value.URL, value.Tag, value.Message, value.Object.Type, value.Object.SHA, value.Object.URL) + identityBytes(value.Tagger) + verificationBytes(value.Verification)
}
func failure(code string) Result { return Result{Diagnostic: code} }
func digest(raw []byte) string   { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func deadlineOr(ctx context.Context, fallback string) string {
	if ctx.Err() != nil {
		return DiagnosticDeadline
	}
	return fallback
}
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func validFixedRef(k kind, ref string) bool {
	if k == kindControlRef {
		return ref == "refs/heads/gardener-release-state/minor" || ref == "refs/heads/gardener-release-state/patch" || ref == "refs/heads/gardener-release-coordination"
	}
	return (k == kindBranchRef && strings.HasPrefix(ref, "refs/heads/")) || (k == kindTagRef && strings.HasPrefix(ref, "refs/tags/"))
}
func validOID(v string) bool {
	if len(v) != 40 {
		return false
	}
	for _, r := range v {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
