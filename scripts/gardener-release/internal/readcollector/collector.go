// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package readcollector contains the isolated, read-only v3 GitHub collector.
// It deliberately has no production constructor. A future authenticated
// snapshot assembler belongs in this package, so callers cannot obtain its
// transport, manufacture a request, or inspect mutable predecessor evidence.
package readcollector

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

// Handle is opaque continuation evidence. Its fields cannot be constructed or
// changed outside this internal package. A handle is accepted only by its
// exact successor on the session which issued it.
type Handle struct {
	session *Session
	nonce   string
}

// Session owns the only transport and mutable artifact store. There is no
// exported constructor: tests construct it inside this package, and production
// construction remains deferred until credentials and policy are reviewed.
type Session struct {
	transport     http.RoundTripper
	now           func() time.Time
	mu            sync.Mutex
	artifacts     map[string]*artifact
	reads         int
	retainedBytes int
	closed        bool
}

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

func newSession(transport http.RoundTripper, now func() time.Time) *Session {
	return &Session{transport: transport, now: now, artifacts: make(map[string]*artifact)}
}

// ReadMinorStateRef, ReadPatchStateRef, and ReadCoordinationRef are the only
// root reads. They own fixed refs; no caller supplies a path or URL.
func (s *Session) ReadMinorStateRef(ctx context.Context, deadline time.Time) (Handle, Result) {
	return s.readFixedRef(ctx, deadline, "refs/heads/gardener-release-state/minor")
}
func (s *Session) ReadPatchStateRef(ctx context.Context, deadline time.Time) (Handle, Result) {
	return s.readFixedRef(ctx, deadline, "refs/heads/gardener-release-state/patch")
}
func (s *Session) ReadCoordinationRef(ctx context.Context, deadline time.Time) (Handle, Result) {
	return s.readFixedRef(ctx, deadline, "refs/heads/gardener-release-coordination")
}

func (s *Session) readFixedRef(ctx context.Context, deadline time.Time, ref string) (Handle, Result) {
	return s.readRef(ctx, kindControlRef, ref, deadline)
}
func (s *Session) readRef(ctx context.Context, k kind, ref string, deadline time.Time) (Handle, Result) {
	if !validFixedRef(k, ref) {
		return Handle{}, failure(DiagnosticProtocol)
	}
	path := "/repos/" + repository + "/git/ref/" + strings.TrimPrefix(ref, "refs/")
	return s.execute(ctx, deadline, k, http.MethodGet, path, nil, func(raw []byte) (*artifact, bool) {
		value, ok := decodeRef(raw, ref, "commit")
		return &artifact{kind: k, ref: value}, ok
	})
}

// ReadRawCommitForRef can read only the commit named by a live ref Handle.
func (s *Session) ReadRawCommitForRef(ctx context.Context, prior Handle, deadline time.Time) (Handle, Result) {
	ref, ok := s.consumeRef(prior, edgeRaw, kindControlRef, kindBranchRef)
	if !ok || ref.Type != "commit" {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.readRawCommit(ctx, ref.SHA, deadline)
}
func (s *Session) ReadRawCommitParent(ctx context.Context, prior Handle, deadline time.Time) (Handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeParent)
	if !ok || len(commit.Parents) != 1 {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.readRawCommit(ctx, commit.Parents[0], deadline)
}
func (s *Session) readRawCommit(ctx context.Context, oid string, deadline time.Time) (Handle, Result) {
	if !validOID(oid) {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.execute(ctx, deadline, kindRawCommit, http.MethodGet, "/repos/"+repository+"/git/commits/"+oid, nil, func(raw []byte) (*artifact, bool) {
		v, ok := decodeRawCommit(raw, oid)
		return &artifact{kind: kindRawCommit, raw: v}, ok
	})
}
func (s *Session) ReadRESTCommitForRawCommit(ctx context.Context, prior Handle, deadline time.Time) (Handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeRest)
	if !ok {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.execute(ctx, deadline, kindRESTCommit, http.MethodGet, "/repos/"+repository+"/commits/"+commit.SHA, nil, func(raw []byte) (*artifact, bool) {
		v, ok := decodeRESTCommit(raw, commit.SHA)
		return &artifact{kind: kindRESTCommit, rest: v}, ok
	})
}
func (s *Session) ReadGraphQLCommitForRawCommit(ctx context.Context, prior Handle, deadline time.Time) (Handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeGraphQL)
	if !ok {
		return Handle{}, failure(DiagnosticProtocol)
	}
	body := []byte(`{"query":"query StateV3Commit($owner:String!,$name:String!,$oid:GitObjectID!){repository(owner:$owner,name:$name){object(oid:$oid){__typename ... on Commit{oid author{name email date user{__typename login databaseId}} committer{name email date user{__typename login databaseId}} signature{isValid state wasSignedByGitHub signer{__typename login databaseId}}}}}}","variables":{"owner":"DataDog","name":"dd-trace-go","oid":"` + commit.SHA + `"}}`)
	return s.execute(ctx, deadline, kindGraphQLCommit, http.MethodPost, "/graphql", body, func(raw []byte) (*artifact, bool) {
		v, ok := decodeGQLCommit(raw, commit.SHA)
		return &artifact{kind: kindGraphQLCommit, gql: v}, ok
	})
}
func (s *Session) ReadTreeForRawCommit(ctx context.Context, prior Handle, deadline time.Time) (Handle, Result) {
	commit, ok := s.consumeCommit(prior, edgeTree)
	if !ok || !validOID(commit.Tree) {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.execute(ctx, deadline, kindTree, http.MethodGet, "/repos/"+repository+"/git/trees/"+commit.Tree+"?recursive=1", nil, func(raw []byte) (*artifact, bool) {
		v, ok := decodeTree(raw, commit.Tree)
		return &artifact{kind: kindTree, tree: v}, ok
	})
}
func (s *Session) ReadBlobForTreeEntry(ctx context.Context, prior Handle, index int, deadline time.Time) (Handle, Result) {
	entry, ok := s.consumeTreeEntry(prior, index)
	if !ok {
		return Handle{}, failure(DiagnosticProtocol)
	}
	return s.execute(ctx, deadline, kindBlob, http.MethodGet, "/repos/"+repository+"/git/blobs/"+entry.SHA, nil, func(raw []byte) (*artifact, bool) {
		v, ok := decodeBlob(raw, entry.SHA, maxResponseBytes)
		return &artifact{kind: kindBlob, blob: v}, ok
	})
}

// Close deterministically releases every transient evidence artifact retained
// by a partial collection. A closed session has no retained evidence.
func (s *Session) Close() {
	s.mu.Lock()
	clear(s.artifacts)
	s.retainedBytes = 0
	s.closed = true
	s.mu.Unlock()
}

// Release makes an issued handle unavailable to all later continuations.
func (s *Session) Release(handle Handle) {
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
func (s *Session) consumeRef(handle Handle, edge uint8, allowed ...kind) (refEvidence, bool) {
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
func (s *Session) consumeCommit(handle Handle, edge uint8) (commitEvidence, bool) {
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
func (s *Session) consumeTreeEntry(handle Handle, index int) (treeEntry, bool) {
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
func (s *Session) get(handle Handle) *artifact {
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

func (s *Session) retained(handle Handle, artifact *artifact) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.artifacts[handle.nonce] == artifact
}

// The following accessors are intentionally package-private. A future
// assembler belongs beside Session and receives defensive typed copies only;
// raw HTTP bytes and a generic artifact accessor never cross this boundary.
func (s *Session) rawCommitFor(handle Handle) (wireRawCommit, bool) {
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
func (s *Session) restCommitFor(handle Handle) (wireRESTCommit, bool) {
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
func (s *Session) graphQLCommitFor(handle Handle) (wireGQLCommit, bool) {
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
func (s *Session) treeFor(handle Handle) (wireTree, bool) {
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
func (s *Session) blobFor(handle Handle) (wireBlob, bool) {
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

func (s *Session) tagFor(handle Handle) (wireTag, bool) {
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

func (s *Session) execute(ctx context.Context, deadline time.Time, k kind, method, path string, body []byte, decode func([]byte) (*artifact, bool)) (Handle, Result) {
	if deadline.IsZero() || !s.now().Before(deadline) || ctx == nil {
		return Handle{}, failure(DiagnosticDeadline)
	}
	s.mu.Lock()
	if s.closed || s.reads >= maxReads {
		s.mu.Unlock()
		return Handle{}, failure(DiagnosticProtocol)
	}
	s.reads++
	s.mu.Unlock()
	requestContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		obs, retry, delay, result := s.readOnce(requestContext, k, method, path, body, attempt)
		if result.Diagnostic == DiagnosticOK {
			if artifact, ok := decode(obs); ok {
				// Raw bytes remain confined to this stack frame. Their bounded
				// source size is charged to the decoded artifact budget.
				artifact.bytes = len(obs) + len(artifact.blob.Raw)
				return s.issue(artifact, result)
			}
			result.Diagnostic = DiagnosticResponseInvalid
			return Handle{}, result
		}
		if !retry || attempt == maxAttempts {
			return Handle{}, result
		}
		if delay <= 0 || s.now().Add(delay).After(deadline) || !sleep(requestContext, delay) {
			result.Diagnostic = DiagnosticDeadline
			return Handle{}, result
		}
	}
	return Handle{}, failure(DiagnosticProtocol)
}
func (s *Session) readOnce(ctx context.Context, k kind, method, path string, body []byte, attempt int) ([]byte, bool, time.Duration, Result) {
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
func (s *Session) issue(decoded *artifact, r Result) (Handle, Result) {
	if decoded == nil {
		return Handle{}, failure(DiagnosticProtocol)
	}
	bytes := artifactBytes(decoded)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Handle{}, failure(DiagnosticTransport)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.artifacts) >= maxArtifacts || bytes < 0 || bytes > maxRetainedBytes-s.retainedBytes {
		return Handle{}, failure(DiagnosticProtocol)
	}
	a := cloneArtifact(decoded)
	a.bytes = bytes
	a.usedEntries = make(map[int]bool)
	n := hex.EncodeToString(nonce[:])
	s.artifacts[n] = a
	s.retainedBytes += a.bytes
	return Handle{session: s, nonce: n}, r
}

// cloneArtifact transfers only immutable evidence into Session ownership. It
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
