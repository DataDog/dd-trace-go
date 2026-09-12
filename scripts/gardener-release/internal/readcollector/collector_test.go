// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package readcollector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const oid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func refJSON(ref string) string {
	return `{"ref":"` + ref + `","node_id":"n","url":"u","object":{"sha":"` + oid + `","type":"commit","url":"u"}}`
}
func TestClosedRootReadPinsRequest(t *testing.T) {
	s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.github.com/repos/DataDog/dd-trace-go/git/ref/heads/gardener-release-state/minor" || r.Header.Get("Authorization") != "" || r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("X-GitHub-Api-Version") != apiVersion {
			t.Fatal("unclosed request")
		}
		return response(refJSON("refs/heads/gardener-release-state/minor")), nil
	}), time.Now)
	h, r := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	if r.Diagnostic != "" || h.session != s {
		t.Fatalf("%+v", r)
	}
	s.Release(h)
}
func TestForgedHandleCannotRead(t *testing.T) {
	calls := 0
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("no") }), time.Now)
	_, r := s.ReadRawCommitForRef(context.Background(), Handle{}, time.Now().Add(time.Second))
	if r.Diagnostic != DiagnosticProtocol || calls != 0 {
		t.Fatal(r)
	}
}
func TestRootContinuationIsSingleUse(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return response(refJSON("refs/heads/gardener-release-state/minor")), nil
		}
		return response(`{"sha":"` + oid + `","node_id":"n","url":"u","html_url":"u","author":{},"committer":{},"tree":{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"u"},"message":"x","parents":[],"verification":{}}`), nil
	}), time.Now)
	h, _ := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	var wg sync.WaitGroup
	ok := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, r := s.ReadRawCommitForRef(context.Background(), h, time.Now().Add(time.Second))
			if r.Diagnostic == "" {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok > 1 || calls > 2 {
		t.Fatalf("ok=%d calls=%d", ok, calls)
	}
}
func TestDeadlineAndMalformedBodyFailClosed(t *testing.T) {
	s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }), time.Now)
	_, r := s.ReadMinorStateRef(context.Background(), time.Now().Add(10*time.Millisecond))
	if r.Diagnostic != DiagnosticDeadline {
		t.Fatal(r)
	}
	s = newSession(roundTrip(func(*http.Request) (*http.Response, error) { return response(`{"data":{}}`), nil }), time.Now)
	_, r = s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	if r.Diagnostic != DiagnosticResponseInvalid {
		t.Fatal(r)
	}
}
func TestSessionCloseReleasesPartialCollection(t *testing.T) {
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-state/minor")), nil
	}), time.Now)
	h, result := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	if result.Diagnostic != "" || h.session != s || len(s.artifacts) != 1 {
		t.Fatalf("unexpected partial collection: %#v", result)
	}
	s.Close()
	if len(s.artifacts) != 0 || s.retainedBytes != 0 {
		t.Fatal("close retained partial evidence")
	}
	if _, result := s.ReadRawCommitForRef(context.Background(), h, time.Now().Add(time.Second)); result.Diagnostic != DiagnosticProtocol {
		t.Fatal("closed session retained a handle")
	}
}

func TestTreeEntryContinuationIsSingleUse(t *testing.T) {
	entryOID := gitBlobOID([]byte("x"))
	s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/"):
			return response(refJSON("refs/heads/gardener-release-state/minor")), nil
		case strings.Contains(r.URL.Path, "/git/commits/"):
			return response(wireRawJSON()), nil
		case strings.Contains(r.URL.Path, "/git/trees/"):
			return response(`{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[{"path":"x","mode":"100644","type":"blob","sha":"` + entryOID + `","size":1,"url":"u"}]}`), nil
		default:
			return response(`{"sha":"` + entryOID + `","node_id":"n","size":1,"url":"u","content":"eA==","encoding":"base64"}`), nil
		}
	}), time.Now)
	root, _ := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	commit, _ := s.ReadRawCommitForRef(context.Background(), root, time.Now().Add(time.Second))
	tree, _ := s.ReadTreeForRawCommit(context.Background(), commit, time.Now().Add(time.Second))
	blob, result := s.ReadBlobForTreeEntry(context.Background(), tree, 0, time.Now().Add(time.Second))
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	s.Release(blob)
	if _, result := s.ReadBlobForTreeEntry(context.Background(), tree, 0, time.Now().Add(time.Second)); result.Diagnostic != DiagnosticProtocol {
		t.Fatal("tree entry replayed")
	}
}

func TestCollectorRejectsMalformedEvidenceWithoutHandle(t *testing.T) {
	t.Run("raw identity", func(t *testing.T) {
		s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "/git/ref/") {
				return response(refJSON("refs/heads/gardener-release-state/minor")), nil
			}
			return response(strings.Replace(wireRawJSON(), `"email":"bot@example.invalid"`, `"email":"bad"`, 1)), nil
		}), time.Now)
		ref, result := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
		if result.Diagnostic != "" {
			t.Fatal(result)
		}
		handle, result := s.ReadRawCommitForRef(context.Background(), ref, time.Now().Add(time.Second))
		if result.Diagnostic != DiagnosticResponseInvalid || handle.session != nil || len(s.artifacts) != 0 {
			t.Fatalf("invalid raw evidence issued a handle: %#v %#v", handle, result)
		}
	})
	t.Run("tree path", func(t *testing.T) {
		s := newSession(roundTrip(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(r.URL.Path, "/git/ref/"):
				return response(refJSON("refs/heads/gardener-release-state/minor")), nil
			case strings.Contains(r.URL.Path, "/git/commits/"):
				return response(wireRawJSON()), nil
			default:
				return response(`{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[{"path":"../state.json","mode":"100644","type":"blob","sha":"` + wireOID + `","size":1,"url":"u"}]}`), nil
			}
		}), time.Now)
		ref, _ := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
		commit, result := s.ReadRawCommitForRef(context.Background(), ref, time.Now().Add(time.Second))
		if result.Diagnostic != "" {
			t.Fatal(result)
		}
		handle, result := s.ReadTreeForRawCommit(context.Background(), commit, time.Now().Add(time.Second))
		if result.Diagnostic != DiagnosticResponseInvalid || handle.session != nil || len(s.artifacts) != 1 {
			t.Fatalf("unsafe tree evidence issued a handle: %#v %#v", handle, result)
		}
	})
}

func TestNoExportedConstructorOrGenericDispatch(t *testing.T) { // Compile-time API shape: only opaque Handle and narrow methods exist.
	var _ func(*Session, context.Context, time.Time) (Handle, Result) = (*Session).ReadMinorStateRef
	var _ func(*Session, Handle) = (*Session).Release
	var _ func(*Session) = (*Session).Close
}

func pointer[T any](value T) *T { return &value }

func TestArtifactAccessorsDeepCopyAllRetainedEvidence(t *testing.T) {
	s := newSession(nil, time.Now)

	raw, ok := decodeRawCommit([]byte(wireRawJSON()), wireOID)
	if !ok {
		t.Fatal("raw fixture")
	}
	rawHandle, result := s.issue(&artifact{kind: kindRawCommit, raw: raw}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	rawCopy, ok := s.rawCommitFor(rawHandle)
	if !ok {
		t.Fatal("raw accessor")
	}
	rawCopy.Parents[0].SHA = wireTreeOID
	*rawCopy.Verification.Signature = "changed"
	rawAgain, _ := s.rawCommitFor(rawHandle)
	if rawAgain.Parents[0].SHA != wireParentOID || *rawAgain.Verification.Signature != "sig" {
		t.Fatal("raw accessor aliases retained evidence")
	}

	rest, ok := decodeRESTCommit([]byte(strings.Replace(wireRESTWithFileJSON(), `"contents_url":"u"}`, `"contents_url":"u","patch":"patch","previous_filename":"old.go"}`, 1)), wireOID)
	if !ok {
		t.Fatal("rest fixture")
	}
	restHandle, result := s.issue(&artifact{kind: kindRESTCommit, rest: rest}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	restCopy, ok := s.restCommitFor(restHandle)
	if !ok {
		t.Fatal("rest accessor")
	}
	*restCopy.Author.SiteAdmin = true
	*restCopy.Commit.Verification.Payload = "changed"
	*restCopy.Files[0].Additions = 99
	*restCopy.Files[0].Patch = "changed"
	*restCopy.Files[0].PreviousFilename = "changed"
	restAgain, _ := s.restCommitFor(restHandle)
	if *restAgain.Author.SiteAdmin || *restAgain.Commit.Verification.Payload != "payload" || *restAgain.Files[0].Additions != 1 || *restAgain.Files[0].Patch != "patch" || *restAgain.Files[0].PreviousFilename != "old.go" {
		t.Fatal("REST accessor aliases retained evidence")
	}

	gql, ok := decodeGQLCommit([]byte(wireGraphQLJSON()), wireOID)
	if !ok {
		t.Fatal("graphql fixture")
	}
	gqlHandle, result := s.issue(&artifact{kind: kindGraphQLCommit, gql: gql}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	gqlCopy, ok := s.graphQLCommitFor(gqlHandle)
	if !ok {
		t.Fatal("graphql accessor")
	}
	gqlCopy.Data.Repository.Object.Author.User.Login = "changed"
	*gqlCopy.Data.Repository.Object.Signature.IsValid = false
	gqlAgain, _ := s.graphQLCommitFor(gqlHandle)
	if gqlAgain.Data.Repository.Object.Author.User.Login != "bot" || !*gqlAgain.Data.Repository.Object.Signature.IsValid {
		t.Fatal("GraphQL accessor aliases retained evidence")
	}

	tree, ok := decodeTree([]byte(`{"sha":"`+wireTreeOID+`","url":"u","truncated":false,"tree":[{"path":"file","mode":"100644","type":"blob","sha":"`+wireOID+`","size":1,"url":"u"}]}`), wireTreeOID)
	if !ok {
		t.Fatal("tree fixture")
	}
	treeHandle, result := s.issue(&artifact{kind: kindTree, tree: tree}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	treeCopy, ok := s.treeFor(treeHandle)
	if !ok {
		t.Fatal("tree accessor")
	}
	*treeCopy.Tree[0].Size = 99
	treeAgain, _ := s.treeFor(treeHandle)
	if *treeAgain.Tree[0].Size != 1 {
		t.Fatal("tree accessor aliases retained evidence")
	}

	blobRaw := []byte("x")
	blobOID := gitBlobOID(blobRaw)
	blob, ok := decodeBlob([]byte(`{"sha":"`+blobOID+`","node_id":"n","size":1,"url":"u","content":"eA==","encoding":"base64"}`), blobOID, 16)
	if !ok {
		t.Fatal("blob fixture")
	}
	blobHandle, result := s.issue(&artifact{kind: kindBlob, blob: blob}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	blobCopy, ok := s.blobFor(blobHandle)
	if !ok {
		t.Fatal("blob accessor")
	}
	blobCopy.Raw[0] = 'z'
	blobAgain, _ := s.blobFor(blobHandle)
	if string(blobAgain.Raw) != "x" {
		t.Fatal("blob accessor aliases retained evidence")
	}

	tag, ok := decodeTag([]byte(`{"node_id":"n","sha":"`+wireTreeOID+`","url":"u","tag":"v1.2.3","message":"v1.2.3\n","tagger":`+wireIdentityJSON()+`,"object":{"type":"commit","sha":"`+wireOID+`","url":"u"},"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`), wireTreeOID, "v1.2.3", wireOID)
	if !ok {
		t.Fatal("tag fixture")
	}
	tagHandle, result := s.issue(&artifact{kind: kindTagObject, tag: tag}, Result{})
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	tagCopy, ok := s.tagFor(tagHandle)
	if !ok {
		t.Fatal("tag accessor")
	}
	tagCopy.Verification.Signature = pointer("forged")
	tagAgain, _ := s.tagFor(tagHandle)
	if tagAgain.Verification.Signature != nil {
		t.Fatal("tag accessor aliases retained evidence")
	}
}

func TestArtifactBudgetCountsVerificationAndCleansUp(t *testing.T) {
	s := newSession(nil, time.Now)
	large := strings.Repeat("x", 6<<20)
	rawJSON := strings.Replace(strings.Replace(wireRawJSON(), `"signature":"sig"`, `"signature":"`+large+`"`, 1), `"payload":"payload"`, `"payload":"`+large+`"`, 1)
	raw, ok := decodeRawCommit([]byte(rawJSON), wireOID)
	if !ok {
		t.Fatal("large valid raw fixture rejected")
	}
	if _, result := s.issue(&artifact{kind: kindRawCommit, raw: raw}, Result{}); result.Diagnostic != "" {
		t.Fatalf("first retained raw artifact: %#v", result)
	}
	if _, result := s.issue(&artifact{kind: kindRawCommit, raw: raw}, Result{}); result.Diagnostic != "" {
		t.Fatalf("second retained raw artifact: %#v", result)
	}
	if _, result := s.issue(&artifact{kind: kindRawCommit, raw: raw}, Result{}); result.Diagnostic != DiagnosticProtocol {
		t.Fatalf("accepted raw verification payloads over byte budget: %#v", result)
	}
	if s.retainedBytes <= 2*(12<<20) {
		t.Fatalf("verification bytes were not charged: %d", s.retainedBytes)
	}
	s.Close()
	if s.retainedBytes != 0 || len(s.artifacts) != 0 {
		t.Fatal("close did not release byte accounting")
	}
}

func TestArtifactBudgetCountsRESTVerification(t *testing.T) {
	s := newSession(nil, time.Now)
	large := strings.Repeat("x", 6<<20)
	restJSON := strings.Replace(strings.Replace(wireRESTJSON(), `"signature":"sig"`, `"signature":"`+large+`"`, 1), `"payload":"payload"`, `"payload":"`+large+`"`, 1)
	rest, ok := decodeRESTCommit([]byte(restJSON), wireOID)
	if !ok {
		t.Fatal("large valid REST fixture rejected")
	}
	for count := 0; count < 2; count++ {
		if _, result := s.issue(&artifact{kind: kindRESTCommit, rest: rest}, Result{}); result.Diagnostic != "" {
			t.Fatalf("REST artifact %d: %#v", count, result)
		}
	}
	if _, result := s.issue(&artifact{kind: kindRESTCommit, rest: rest}, Result{}); result.Diagnostic != DiagnosticProtocol {
		t.Fatalf("accepted REST verification payloads over byte budget: %#v", result)
	}
	if s.retainedBytes <= 2*(12<<20) {
		t.Fatalf("REST verification bytes were not charged: %d", s.retainedBytes)
	}
	s.Close()
	if s.retainedBytes != 0 || len(s.artifacts) != 0 {
		t.Fatal("close did not release REST byte accounting")
	}
}

func TestRESTCommitWithEmptyFilesIssuesAndReleasesBoundedArtifact(t *testing.T) {
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/git/ref/"):
			return response(refJSON("refs/heads/gardener-release-state/minor")), nil
		case strings.Contains(request.URL.Path, "/git/commits/"):
			return response(wireRawJSON()), nil
		default:
			return response(wireRESTJSON()), nil
		}
	}), time.Now)

	root, result := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	raw, result := s.ReadRawCommitForRef(context.Background(), root, time.Now().Add(time.Second))
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	handle, result := s.ReadRESTCommitForRawCommit(context.Background(), raw, time.Now().Add(time.Second))
	if result.Diagnostic != "" || handle.session != s {
		t.Fatalf("empty files response did not issue REST artifact: %#v %#v", handle, result)
	}
	if s.retainedBytes <= 0 || len(s.artifacts) != 2 {
		t.Fatalf("empty files response did not retain a bounded artifact: bytes=%d artifacts=%d", s.retainedBytes, len(s.artifacts))
	}
	s.Release(handle)
	if len(s.artifacts) != 1 || s.retainedBytes <= 0 {
		t.Fatalf("release did not remove only the REST artifact: bytes=%d artifacts=%d", s.retainedBytes, len(s.artifacts))
	}
	s.Close()
	if s.retainedBytes != 0 || len(s.artifacts) != 0 {
		t.Fatalf("close did not release remaining evidence: bytes=%d artifacts=%d", s.retainedBytes, len(s.artifacts))
	}
}

func TestRESTNegativeStatsProduceNoHandle(t *testing.T) {
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/git/ref/"):
			return response(refJSON("refs/heads/gardener-release-state/minor")), nil
		case strings.Contains(request.URL.Path, "/git/commits/"):
			return response(wireRawJSON()), nil
		default:
			return response(strings.Replace(wireRESTJSON(), `"stats":{"total":0,"additions":0,"deletions":0}`, `"stats":{"total":-1,"additions":-1,"deletions":-1}`, 1)), nil
		}
	}), time.Now)
	root, result := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second))
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	raw, result := s.ReadRawCommitForRef(context.Background(), root, time.Now().Add(time.Second))
	if result.Diagnostic != "" {
		t.Fatal(result)
	}
	handle, result := s.ReadRESTCommitForRawCommit(context.Background(), raw, time.Now().Add(time.Second))
	if result.Diagnostic != DiagnosticResponseInvalid || handle.session != nil {
		t.Fatalf("negative REST stats issued a handle: %#v %#v", handle, result)
	}
}
