// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixtureStateV3WireAddition(path string, raw []byte) StateV3StagedFile {
	digest := sha256.Sum256(raw)
	return StateV3StagedFile{Path: path, Raw: raw, SizeBytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), BlobOID: stateV3GitBlobOID(raw)}
}

func fixtureStateV3PreparedCommitPermit(t *testing.T, record StateV3Record, policy StateV3Policy, envelope StateV3StagedEnvelopeEvidence) (stateV3PreparedCommitPermit, error) {
	t.Helper()
	armed := fixtureStateV3RecordAtEventCount(t, record, 3)
	armed.Prepared = &envelope.Prepared
	armed.TagPlans = envelope.TagPlans
	rebindStateV3Events(t, &armed)
	raw, err := canonicalJSON(armed)
	if err != nil {
		t.Fatal(err)
	}
	return stateV3PreparedCommitPermitForRecord(raw, armed, policy, fixtureStateV3AuthenticationForEnvelope(t, policy, armed, envelope), envelope)
}

// fixtureStateV3AuthenticationForEnvelope replaces the fixture's generated
// staged bytes with the exact envelope being authenticated, then recomputes
// its synthetic tree deltas. This lets the request-cap test exercise the same
// authenticated durable-arm route as production serialization.
func fixtureStateV3AuthenticationForEnvelope(t *testing.T, policy StateV3Policy, record StateV3Record, envelope StateV3StagedEnvelopeEvidence) StateV3Authentication {
	t.Helper()
	authentication := fixtureStateV3Authentication(t, policy, record)
	snapshots := append([]StateV3StateSnapshot{authentication.Current}, authentication.Predecessors...)
	for snapshotIndex := range snapshots {
		snapshot := &snapshots[snapshotIndex]
		if snapshot.StagedEnvelope == nil {
			continue
		}
		staged := cloneStateV3(t, envelope)
		snapshot.StagedEnvelope = &staged
		if snapshot.ActiveRecord.Present {
			activeStaged := cloneStateV3(t, envelope)
			snapshot.ActiveRecord.StagedEnvelope = &activeStaged
		}
		for entryIndex := range snapshot.Tree.Entries {
			for _, file := range envelope.Files {
				if snapshot.Tree.Entries[entryIndex].Path == file.Path {
					snapshot.Tree.Entries[entryIndex].OID = file.BlobOID
				}
			}
		}
	}
	for index := 0; index+1 < len(snapshots); index++ {
		snapshots[index].Commit.ChangedPaths = stateV3TreeChanges(snapshots[index+1].Tree, snapshots[index].Tree)
	}
	authentication.Current = snapshots[0]
	authentication.Predecessors = snapshots[1:]
	return authentication
}

func TestStateV3PreparedCreateCommitRequestIsCanonical(t *testing.T) {
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	envelope := fixtureStateV3StagedEnvelope(t, record)
	permit, err := fixtureStateV3PreparedCommitPermit(t, record, policy, *envelope)
	if err != nil {
		t.Fatalf("permit: %v", err)
	}
	first, err := stateV3BuildPreparedCreateCommitRequest(permit)
	if err != nil {
		t.Fatalf("build first: %v", err)
	}
	second, err := stateV3BuildPreparedCreateCommitRequest(permit)
	if err != nil {
		t.Fatalf("build second: %v", err)
	}
	if !bytes.Equal(first.bytes(), second.bytes()) {
		t.Fatal("prepared request is non-deterministic")
	}
	body := string(first.bytes())
	for _, required := range []string{stateV3CreateCommitQuery, `"repositoryNameWithOwner":"DataDog/dd-trace-go"`, `"branchName":"gardener-release-state/minor"`, `"expectedHeadOid":"` + v3OIDa + `"`, `"headline":"gardener-release state: 123:789"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("canonical request missing %q: %s", required, body)
		}
	}

}

func TestStateV3PreparedCreateCommitRequestRejectsUnauthenticatedEnvelope(t *testing.T) {
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	envelope := fixtureStateV3StagedEnvelope(t, record)
	envelope.Files[0].Raw = []byte("changed")
	armed := fixtureStateV3RecordAtEventCount(t, record, 3)
	raw, err := canonicalJSON(armed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateV3PreparedCommitPermitForRecord(raw, armed, policy, fixtureStateV3Authentication(t, policy, armed), *envelope); err == nil {
		t.Fatal("accepted unauthenticated envelope")
	}
}

func TestStateV3PreparedCreateCommitRequestCapUsesThreeFileEnvelope(t *testing.T) {
	build := func(bundleBytes int) (stateV3PreparedCreateCommitRequest, error) {
		policy, _, envelope := fixtureStateV3WirePreparedEnvelope(t, bundleBytes)
		record := fixtureStateV3Record(t)
		permit, err := fixtureStateV3PreparedCommitPermit(t, record, policy, envelope)
		if err != nil {
			return stateV3PreparedCreateCommitRequest{}, err
		}
		return stateV3BuildPreparedCreateCommitRequest(permit)
	}
	low, high := 1, MaxStateV3DecodedAdditionBytes
	for low < high {
		mid := low + (high-low+1)/2
		if _, err := build(mid); err == nil {
			low = mid
		} else {
			high = mid - 1
		}
	}
	t.Logf("maximum accepted bundle=%d", low)
	request, err := build(low)
	if err != nil || len(request.bytes()) > MaxStateV3GraphQLRequestBytes {
		t.Fatalf("exact three-file boundary rejected: bytes=%d err=%v", len(request.bytes()), err)
	}
	t.Logf("three-file serialized boundary: bundle=%d request=%d cap=%d", low, len(request.bytes()), MaxStateV3GraphQLRequestBytes)
	if _, err := build(low + 1); err == nil {
		t.Fatal("accepted one-byte-over exact three-file envelope")
	}
	policy, _, envelope := fixtureStateV3WirePreparedEnvelope(t, 1)
	envelope.Files = append(envelope.Files, fixtureStateV3WireAddition("requests/123/789/extra", []byte("x")))
	record := fixtureStateV3Record(t)
	if _, err := fixtureStateV3PreparedCommitPermit(t, record, policy, envelope); err == nil {
		t.Fatal("accepted non-three-file prepared envelope")
	}
}

// fixtureStateV3WirePreparedEnvelope builds the exact authenticated envelope
// shape while varying only its bundle bytes. It is intentionally not a generic
// request fixture: every returned file is re-bound to the prepared manifest.
func fixtureStateV3WirePreparedEnvelope(t *testing.T, bundleBytes int) (StateV3Policy, StateV3Reservation, StateV3StagedEnvelopeEvidence) {
	t.Helper()
	if bundleBytes < 1 {
		t.Fatal("invalid bundle size")
	}
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	prepared := *record.Prepared
	bundle := bytes.Repeat([]byte{'x'}, bundleBytes)
	reservationRaw, err := canonicalJSON(record.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 10; attempt++ {
		digest := sha256.Sum256(bundle)
		prepared.Bundle.SHA256 = hex.EncodeToString(digest[:])
		prepared.Bundle.BlobOID = stateV3GitBlobOID(bundle)
		prepared.Bundle.SizeBytes = int64(len(bundle))
		manifest := stateV3PreparedManifest{SchemaVersion: StateV3SchemaVersion, Bundle: prepared.Bundle, AdditionCount: prepared.AdditionCount, TotalDecodedAdditionBytes: prepared.TotalDecodedAdditionBytes, ToolDigest: prepared.ToolDigest, ValidatorDigest: prepared.ValidatorDigest, Mutation: prepared.Mutation, TagPlans: record.TagPlans}
		preparedRaw, err := canonicalJSON(manifest)
		if err != nil {
			t.Fatal(err)
		}
		total := int64(len(bundle) + len(preparedRaw) + len(reservationRaw))
		if total != prepared.TotalDecodedAdditionBytes {
			prepared.TotalDecodedAdditionBytes = total
			continue
		}
		raws := [][]byte{bundle, preparedRaw, reservationRaw}
		files := make([]StateV3StagedFile, len(raws))
		for i, raw := range raws {
			files[i] = fixtureStateV3WireAddition(prepared.StateFiles[i].Path, raw)
			prepared.StateFiles[i] = StateV3PreparedFile{Path: files[i].Path, SHA256: files[i].SHA256, BlobOID: files[i].BlobOID, SizeBytes: files[i].SizeBytes}
		}
		return policy, record.Reservation, StateV3StagedEnvelopeEvidence{Prepared: prepared, TagPlans: record.TagPlans, Files: files}
	}
	t.Fatal("prepared envelope size did not converge")
	return StateV3Policy{}, StateV3Reservation{}, StateV3StagedEnvelopeEvidence{}
}

func TestStateV3FixedReadRequests(t *testing.T) {
	ref, err := stateV3RESTRefRead(StateV3MinorStateRef)
	if err != nil || ref.method != "GET" || ref.path != "/repos/DataDog/dd-trace-go/git/ref/heads/gardener-release-state/minor" {
		t.Fatalf("ref request=%#v err=%v", ref, err)
	}
	for name, build := range map[string]func(string) (stateV3ReadRequest, error){"raw": stateV3RawCommitRead, "rest": stateV3RESTCommitRead, "tree": stateV3TreeRead, "blob": stateV3BlobRead, "tag": stateV3AnnotatedTagRead, "graphql": stateV3GraphQLCommitRead} {
		t.Run(name, func(t *testing.T) {
			request, err := build(v3OIDa)
			if err != nil || request.method == "" || request.path == "" || (name == "graphql" && !bytes.Contains(request.body, []byte(stateV3CommitQuery))) {
				t.Fatalf("request=%#v err=%v", request, err)
			}
		})
	}
	if _, err := stateV3RESTRefRead("refs/heads/main"); err == nil {
		t.Fatal("accepted arbitrary ref")
	}
	if _, err := stateV3GraphQLCommitRead("bad"); err == nil {
		t.Fatal("accepted invalid object id")
	}
}

func TestStateV3PreparedCommitPermitRequiresAuthenticatedRecord(t *testing.T) {
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	envelope := fixtureStateV3StagedEnvelope(t, record)
	armed := fixtureStateV3RecordAtEventCount(t, record, 3)
	raw, err := canonicalJSON(armed)
	if err != nil {
		t.Fatal(err)
	}
	authentication := fixtureStateV3Authentication(t, policy, armed)
	if _, err := stateV3PreparedCommitPermitForRecord(raw, armed, policy, authentication, *envelope); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]byte, *StateV3Authentication){
		"raw mismatch": func(raw []byte, _ *StateV3Authentication) { raw[0] ^= 1 },
		"authentication mismatch": func(_ []byte, authentication *StateV3Authentication) {
			authentication.Current.RawRecord = []byte("different")
		},
		"coordination failure": func(_ []byte, authentication *StateV3Authentication) { authentication.Coordination = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidateRaw := append([]byte(nil), raw...)
			candidateAuthentication := cloneStateV3(t, authentication)
			mutate(candidateRaw, &candidateAuthentication)
			if _, err := stateV3PreparedCommitPermitForRecord(candidateRaw, armed, policy, candidateAuthentication, *envelope); err == nil {
				t.Fatal("accepted unauthenticated record")
			}
		})
	}
	armed.MutationArms[len(armed.MutationArms)-1].ExpectedOldOID = v3OIDb
	if _, err := stateV3PreparedCommitPermitForRecord(raw, armed, policy, fixtureStateV3Authentication(t, policy, armed), *envelope); err == nil {
		t.Fatal("accepted mismatched arm")
	}
	withoutArm := fixtureStateV3RecordAtEventCount(t, record, 2)
	withoutArmRaw, err := canonicalJSON(withoutArm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateV3PreparedCommitPermitForRecord(withoutArmRaw, withoutArm, policy, fixtureStateV3Authentication(t, policy, withoutArm), *envelope); err == nil {
		t.Fatal("accepted missing arm")
	}
}

func TestStateV3PreparedCreateCommitRequestRejectsFabricatedPermit(t *testing.T) {
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	envelope := fixtureStateV3StagedEnvelope(t, record)
	permit, err := fixtureStateV3PreparedCommitPermit(t, record, policy, *envelope)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*stateV3PreparedCommitPermit){
		"main ref":             func(value *stateV3PreparedCommitPermit) { value.stateRef = "refs/heads/main" },
		"substituted oid":      func(value *stateV3PreparedCommitPermit) { value.expectedOID = v3OIDb },
		"substituted envelope": func(value *stateV3PreparedCommitPermit) { value.envelope.Files[0].Raw = []byte("changed") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := permit
			mutate(&candidate)
			if _, err := stateV3BuildPreparedCreateCommitRequest(candidate); err == nil {
				t.Fatal("accepted fabricated permit")
			}
		})
	}
}

func TestStateV3IntentDerivedRefReads(t *testing.T) {
	policy := fixtureStateV3Policy()
	full := fixtureStateV3Record(t)
	armed := fixtureStateV3RecordAtEventCount(t, full, 3)
	branch, err := stateV3BranchRefRead(armed, policy, 0)
	if err != nil || branch.path != "/repos/DataDog/dd-trace-go/git/ref/heads/release-v2.11.x" {
		t.Fatalf("branch=%#v err=%v", branch, err)
	}
	tag, err := stateV3TagRefRead(full, policy, 0)
	if err != nil || tag.path != "/repos/DataDog/dd-trace-go/git/ref/tags/v2.11.0-rc.1" {
		t.Fatalf("tag=%#v err=%v", tag, err)
	}
	armed.Prepared.Mutation.Branches[0].Ref = "refs/heads/main"
	if _, err := stateV3BranchRefRead(armed, policy, 0); err == nil {
		t.Fatal("accepted arbitrary branch")
	}
	full.TagIntents[0].Ref = "refs/tags/arbitrary"
	if _, err := stateV3TagRefRead(full, policy, 0); err == nil {
		t.Fatal("accepted arbitrary tag")
	}
}

func wireRefJSON() string {
	return `{"ref":"refs/heads/release-v2.1.x","node_id":"REF_kwDO","url":"https://api.github.com/repos/DataDog/dd-trace-go/git/refs/heads/release-v2.1.x","object":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","type":"commit","url":"https://api.github.com/repos/DataDog/dd-trace-go/git/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
}
func wireIdentityJSON() string {
	return `{"name":"Release Bot","email":"release-bot@users.noreply.github.com","date":"2026-09-12T06:30:00Z"}`
}
func wireVerificationJSON() string {
	return `{"verified":true,"reason":"valid","signature":"sig","payload":"payload","verified_at":"2026-09-12T06:30:01Z"}`
}
func wireRawCommitJSON() string {
	return `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","node_id":"C_kwDO","url":"https://api.github.com/repos/DataDog/dd-trace-go/git/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","html_url":"https://github.com/DataDog/dd-trace-go/commit/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author":` + wireIdentityJSON() + `,"committer":` + wireIdentityJSON() + `,"tree":{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://api.github.com/tree"},"message":"release: v2.1.0","parents":[{"sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/parent","html_url":"https://github.com/parent"}],"verification":` + wireVerificationJSON() + `}`
}
func wireRESTIdentityJSON() string {
	return `{"login":"release-bot","id":100,"node_id":"MDQ6","avatar_url":"https://avatars","gravatar_id":"","html_url":"https://github.com/release","url":"https://api.github.com/users/release","followers_url":"https://api.github.com/followers","following_url":"https://api.github.com/following{/other_user}","gists_url":"https://api.github.com/gists{/gist_id}","starred_url":"https://api.github.com/starred{/owner}{/repo}","subscriptions_url":"https://api.github.com/subscriptions","organizations_url":"https://api.github.com/orgs","repos_url":"https://api.github.com/repos","events_url":"https://api.github.com/events{/privacy}","received_events_url":"https://api.github.com/received_events","type":"User","user_view_type":"public","site_admin":false}`
}
func wireRESTCommitJSON() string {
	return `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","node_id":"C_kwDO","url":"https://api.github.com/commit","html_url":"https://github.com/commit","comments_url":"https://api.github.com/comments","commit":{"author":` + wireIdentityJSON() + `,"committer":` + wireIdentityJSON() + `,"message":"release: v2.1.0","tree":{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://api.github.com/tree"},"url":"https://api.github.com/git/commit","comment_count":0,"verification":` + wireVerificationJSON() + `},"author":` + wireRESTIdentityJSON() + `,"committer":null,"parents":[{"sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/parent","html_url":"https://github.com/parent"}],"stats":{"total":1,"additions":1,"deletions":0},"files":[{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","filename":"version.go","status":"modified","additions":1,"deletions":0,"changes":1,"blob_url":"https://github.com/blob","raw_url":"https://github.com/raw","contents_url":"https://api.github.com/content","patch":"@@"}]}`
}
func wireGraphQLCommitJSON() string {
	return `{"data":{"repository":{"object":{"__typename":"Commit","oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author":{"name":"Release Bot","email":"release-bot@users.noreply.github.com","date":"2026-09-12T06:30:00Z","user":{"__typename":"User","login":"release-bot","databaseId":100}},"committer":{"name":"Release Bot","email":"release-bot@users.noreply.github.com","date":"2026-09-12T06:30:00Z","user":null},"signature":{"isValid":true,"state":"VALID","wasSignedByGitHub":true,"signer":{"__typename":"User","login":"release-bot","databaseId":100}}}}}}`
}

func TestStateV3WireDecodesCapturedEndpointShapes(t *testing.T) {
	ref, err := decodeStateV3WireRef([]byte(wireRefJSON()), "refs/heads/release-v2.1.x", "commit")
	if err != nil || !stateV3ExactRefReread(ref, ref.Ref, v3OIDa, "commit") {
		t.Fatalf("ref: %v", err)
	}
	commit, err := decodeStateV3WireCommit([]byte(wireRawCommitJSON()), v3OIDa)
	if err != nil || !stateV3ExactCommitReread(commit, v3OIDa, v3OIDc, v3OIDb, "release: v2.1.0") {
		t.Fatalf("raw commit: %v", err)
	}
	rest, err := decodeStateV3WireRESTCommit([]byte(wireRESTCommitJSON()), v3OIDa)
	if err != nil {
		t.Fatalf("REST commit: %v", err)
	}
	captured, err := os.ReadFile("testdata/state_v3_wire/rest_commit_captured.json")
	if err != nil {
		t.Fatal(err)
	}
	var capturedDocument struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(captured, &capturedDocument); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStateV3WireRESTCommit(captured, capturedDocument.SHA); err != nil {
		t.Fatalf("sanitized captured REST commit: %v", err)
	}
	_, author, committer, ok := rest.roles()
	if !ok || author.Login != "release-bot" || committer.Present {
		t.Fatal("REST identities lost")
	}
	graphql, err := decodeStateV3WireGraphQLCommit([]byte(wireGraphQLCommitJSON()), v3OIDa)
	if err != nil {
		t.Fatalf("GraphQL commit: %v", err)
	}
	capturedGraphQL, err := os.ReadFile("testdata/state_v3_wire/graphql_commit_captured.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStateV3WireGraphQLCommit(capturedGraphQL, v3OIDa); err != nil {
		t.Fatalf("sanitized captured GraphQL commit: %v", err)
	}
	authorGQL, committerGQL, signer, ok := graphql.roles()
	if !ok || authorGQL.DatabaseID != "100" || committerGQL.Present || signer.Login != "release-bot" {
		t.Fatal("GraphQL identities lost")
	}
}

func TestStateV3WireRejectsHostileJSON(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate":     strings.Replace(wireRefJSON(), `"ref":`, `"ref":"refs/heads/release-v2.1.x","ref":`, 1),
		"unknown":       strings.TrimSuffix(wireRefJSON(), "}") + `,"x":1}`,
		"null required": strings.Replace(wireRefJSON(), `"ref":"refs/heads/release-v2.1.x"`, `"ref":null`, 1),
		"trailing":      wireRefJSON() + ` {}`,
		"wrong type":    strings.Replace(wireRefJSON(), `"type":"commit"`, `"type":"tag"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStateV3WireRef([]byte(raw), "refs/heads/release-v2.1.x", "commit"); err == nil {
				t.Fatal("accepted hostile response")
			}
		})
	}
	if _, err := decodeStateV3WireGraphQLCommit([]byte(strings.Replace(wireGraphQLCommitJSON(), `"databaseId":100`, `"database_id":"100"`, 1)), v3OIDa); err == nil {
		t.Fatal("accepted non-GitHub GraphQL spelling")
	}
}

func TestStateV3WireTreeBlobAndTagReads(t *testing.T) {
	blob := fixtureStateV3WireAddition("internal/version/version.go", []byte("package version\n"))
	blobJSON := `{"sha":"` + blob.BlobOID + `","node_id":"B_kwDO","size":16,"url":"https://api.github.com/blob","content":"cGFja2FnZSB2ZXJzaW9uCg==","encoding":"base64"}`
	decoded, err := decodeStateV3WireBlob([]byte(blobJSON), blob.BlobOID, 1024)
	if err != nil || !stateV3ExactBlobReread(decoded, blob.BlobOID, blob.Raw) {
		t.Fatalf("blob: %v", err)
	}
	treeJSON := `{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://api.github.com/tree","truncated":false,"tree":[{"path":"internal","mode":"040000","type":"tree","sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/tree/internal"},{"path":"internal/version/version.go","mode":"100644","type":"blob","sha":"` + blob.BlobOID + `","size":16,"url":"https://api.github.com/blob"}]}`
	tree, err := decodeStateV3WireTree([]byte(treeJSON), v3OIDb)
	if err != nil || !stateV3ExactTreeReread(tree, v3OIDb, []StateV3StateTreeEntry{{Path: "internal/version/version.go", Mode: "100644", Type: "blob", OID: blob.BlobOID}}) {
		t.Fatalf("tree: %v", err)
	}
	tagJSON := `{"node_id":"TA_kwDO","sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/tag","tag":"v2.1.0","message":"v2.1.0\n","tagger":` + wireIdentityJSON() + `,"object":{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://api.github.com/commit"},"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`
	if _, err := decodeStateV3WireAnnotatedTag([]byte(tagJSON), v3OIDc, "v2.1.0", v3OIDa); err != nil {
		t.Fatalf("tag: %v", err)
	}
	for name, malformed := range map[string]string{
		"tree truncated null":   strings.Replace(treeJSON, `"truncated":false`, `"truncated":null`, 1),
		"tree size null":        strings.Replace(treeJSON, `"size":16`, `"size":null`, 1),
		"tag verified null":     strings.Replace(tagJSON, `"verified":false`, `"verified":null`, 1),
		"tag empty signature":   strings.Replace(tagJSON, `"signature":null`, `"signature":""`, 1),
		"tag nonnull timestamp": strings.Replace(tagJSON, `"verified_at":null`, `"verified_at":"2026-09-12T06:30:01Z"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(name, "tag") {
				if _, err := decodeStateV3WireAnnotatedTag([]byte(malformed), v3OIDc, "v2.1.0", v3OIDa); err == nil {
					t.Fatal("accepted malformed tag")
				}
			} else if _, err := decodeStateV3WireTree([]byte(malformed), v3OIDb); err == nil {
				t.Fatal("accepted malformed tree")
			}
		})
	}
	forbidden := strings.Replace(treeJSON, `"mode":"100644"`, `"mode":"100755"`, 1)
	decodedTree, err := decodeStateV3WireTree([]byte(forbidden), v3OIDb)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodedTree.regularFileEntries(); ok {
		t.Fatal("projected executable as regular state file")
	}
	nestedTreeJSON := `{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://api.github.com/tree","truncated":false,"tree":[{"path":"release-lines","mode":"040000","type":"tree","sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/tree/release-lines"},{"path":"release-lines/2.11.json","mode":"100644","type":"blob","sha":"` + blob.BlobOID + `","size":16,"url":"https://api.github.com/blob"},{"path":"requests","mode":"040000","type":"tree","sha":"dddddddddddddddddddddddddddddddddddddddd","url":"https://api.github.com/tree/requests"},{"path":"requests/123/789/prepared.json","mode":"100644","type":"blob","sha":"` + blob.BlobOID + `","size":16,"url":"https://api.github.com/blob"}]}`
	nested, err := decodeStateV3WireTree([]byte(nestedTreeJSON), v3OIDb)
	if err != nil {
		t.Fatalf("nested state/coordination tree: %v", err)
	}
	entries, ok := nested.regularFileEntries()
	if !ok || len(entries) != 2 {
		t.Fatal("nested tree did not project regular leaves")
	}
}

func removeStateV3WireJSONPath(t *testing.T, raw []byte, path ...string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	current := document
	for _, segment := range path[:len(path)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			t.Fatalf("missing object at %q", segment)
		}
		current = next
	}
	delete(current, path[len(path)-1])
	result, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStateV3WireRequiresNullableAndRESTStructuralFields(t *testing.T) {
	for name, path := range map[string][]string{
		"missing stats":             {"stats"},
		"missing files":             {"files"},
		"missing author":            {"author"},
		"missing committer":         {"committer"},
		"missing author site admin": {"author", "site_admin"},
		"missing author gravatar":   {"author", "gravatar_id"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStateV3WireRESTCommit(removeStateV3WireJSONPath(t, []byte(wireRESTCommitJSON()), path...), v3OIDa); err == nil {
				t.Fatal("accepted missing REST field")
			}
		})
	}
	for name, path := range map[string][]string{
		"missing GraphQL author user":    {"data", "repository", "object", "author", "user"},
		"missing GraphQL committer user": {"data", "repository", "object", "committer", "user"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStateV3WireGraphQLCommit(removeStateV3WireJSONPath(t, []byte(wireGraphQLCommitJSON()), path...), v3OIDa); err == nil {
				t.Fatal("accepted omitted nullable GraphQL association")
			}
		})
	}
}

func TestStateV3WireRecursiveTreeTraversalIsProjectedCanonically(t *testing.T) {
	raw, err := os.ReadFile("testdata/state_v3_wire/tree_recursive_captured.json")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := decodeStateV3WireTree(raw, v3OIDb)
	if err != nil {
		t.Fatalf("inverted traversal rejected: %v", err)
	}
	entries, ok := tree.regularFileEntries()
	if !ok || len(entries) != 4 || entries[0].Path != "a-first.txt" || entries[1].Path != "release-lines/2.11.json" || entries[2].Path != "requests/123/789/prepared.json" || entries[3].Path != "z-last.txt" {
		t.Fatalf("noncanonical projected entries: %#v", entries)
	}
	duplicate := strings.Replace(string(raw), `"path": "requests"`, `"path": "a-first.txt"`, 1)
	if _, err := decodeStateV3WireTree([]byte(duplicate), v3OIDb); err == nil {
		t.Fatal("accepted duplicate recursive-tree path")
	}
}

func TestStateV3WireVerificationAndOptionalRESTFieldsAreStrict(t *testing.T) {
	tagJSON := `{"node_id":"TA_kwDO","sha":"cccccccccccccccccccccccccccccccccccccccc","url":"https://api.github.com/tag","tag":"v2.1.0","message":"v2.1.0\n","tagger":` + wireIdentityJSON() + `,"object":{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://api.github.com/commit"},"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`
	for name, raw := range map[string]string{
		"missing signature":   strings.Replace(tagJSON, `"signature":null,`, "", 1),
		"missing payload":     strings.Replace(tagJSON, `"payload":null,`, "", 1),
		"missing verified at": strings.Replace(tagJSON, `,"verified_at":null`, "", 1),
		"empty payload":       strings.Replace(tagJSON, `"payload":null`, `"payload":""`, 1),
		"signed":              strings.Replace(tagJSON, `"signature":null`, `"signature":"sig"`, 1),
		"verified true":       strings.Replace(tagJSON, `"verified":false`, `"verified":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStateV3WireAnnotatedTag([]byte(raw), v3OIDc, "v2.1.0", v3OIDa); err == nil {
				t.Fatal("accepted malformed unsigned verification")
			}
		})
	}
	withoutPatch := strings.Replace(wireRESTCommitJSON(), `,"patch":"@@"`, "", 1)
	withoutPatch = strings.Replace(withoutPatch, `"status":"modified"`, `"status":"renamed","previous_filename":"old-version.go"`, 1)
	if _, err := decodeStateV3WireRESTCommit([]byte(withoutPatch), v3OIDa); err != nil {
		t.Fatalf("optional patch/renamed file: %v", err)
	}
}

func TestStateV3WireCreateCommitResponseIsStrict(t *testing.T) {
	// This package intentionally has no client, origin, credential, raw
	// observation, or response-to-observed adapter. Only a future durable-arm
	// backend can introduce mutation execution after independent review.
	body := []byte(`{"data":{"createCommitOnBranch":{"commit":{"oid":"` + v3OIDa + `"}}}}`)
	if stateV3ValidatedResponseDigest(body, "") || !stateV3ValidatedResponseDigest(body, stateV3WireBodySHA256(body)) {
		t.Fatal("response digest guard is not strict")
	}
	if err := decodeStateV3WireCreateCommitResponse(body, v3OIDa); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		raw      string
		expected string
	}{
		"empty expected OID":                {string(body), ""},
		"invalid expected OID":              {string(body), "not-an-oid"},
		"missing data":                      {`{}`, v3OIDa},
		"null data":                         {`{"data":null}`, v3OIDa},
		"missing create commit":             {`{"data":{}}`, v3OIDa},
		"null create commit":                {`{"data":{"createCommitOnBranch":null}}`, v3OIDa},
		"missing commit":                    {`{"data":{"createCommitOnBranch":{}}}`, v3OIDa},
		"null commit":                       {`{"data":{"createCommitOnBranch":{"commit":null}}}`, v3OIDa},
		"missing OID":                       {`{"data":{"createCommitOnBranch":{"commit":{}}}}`, v3OIDa},
		"null OID":                          {`{"data":{"createCommitOnBranch":{"commit":{"oid":null}}}}`, v3OIDa},
		"wrong OID type":                    {`{"data":{"createCommitOnBranch":{"commit":{"oid":1}}}}`, v3OIDa},
		"invalid response OID":              {`{"data":{"createCommitOnBranch":{"commit":{"oid":"not-an-oid"}}}}`, v3OIDa},
		"valid but unexpected response OID": {`{"data":{"createCommitOnBranch":{"commit":{"oid":"` + v3OIDb + `"}}}}`, v3OIDa},
	} {
		t.Run(name, func(t *testing.T) {
			if err := decodeStateV3WireCreateCommitResponse([]byte(test.raw), test.expected); err == nil {
				t.Fatal("accepted malformed create-commit response")
			}
		})
	}
}

func TestStateV3WireVisibilitySchedule(t *testing.T) {
	delays, ok := stateV3RefCreateVisibilitySchedule(true)
	if !ok || len(delays) != 4 || delays[0] != 0 || delays[1].Milliseconds() != 500 || delays[2].Milliseconds() != 1500 || delays[3].Milliseconds() != 3000 {
		t.Fatalf("unexpected visibility schedule: %v", delays)
	}
	if _, ok := stateV3RefCreateVisibilitySchedule(false); ok {
		t.Fatal("used 201 schedule without create status")
	}
}
