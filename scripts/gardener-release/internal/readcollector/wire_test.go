// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package readcollector

import (
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"testing"
)

const wireOID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const wireTreeOID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const wireParentOID = "cccccccccccccccccccccccccccccccccccccccc"

func wireIdentityJSON() string {
	return `{"name":"bot","email":"bot@example.invalid","date":"2026-01-02T03:04:05Z"}`
}
func wireVerificationJSON() string {
	return `{"verified":true,"reason":"valid","signature":"sig","payload":"payload","verified_at":"2026-01-02T03:04:05Z"}`
}
func wireRawJSON() string {
	return `{"sha":"` + wireOID + `","node_id":"n","url":"u","html_url":"u","author":` + wireIdentityJSON() + `,"committer":` + wireIdentityJSON() + `,"tree":{"sha":"` + wireTreeOID + `","url":"u"},"message":"message","parents":[{"sha":"` + wireParentOID + `","url":"u","html_url":"u"}],"verification":` + wireVerificationJSON() + `}`
}
func wireRESTUserJSON() string {
	return `{"login":"bot","id":1,"node_id":"n","avatar_url":"u","gravatar_id":"","html_url":"u","url":"u","followers_url":"u","following_url":"u","gists_url":"u","starred_url":"u","subscriptions_url":"u","organizations_url":"u","repos_url":"u","events_url":"u","received_events_url":"u","type":"User","user_view_type":"public","site_admin":false}`
}
func wireRESTJSON() string {
	return `{"sha":"` + wireOID + `","node_id":"n","url":"u","html_url":"u","comments_url":"u","commit":{"author":` + wireIdentityJSON() + `,"committer":` + wireIdentityJSON() + `,"message":"message","tree":{"sha":"` + wireTreeOID + `","url":"u"},"url":"u","comment_count":0,"verification":` + wireVerificationJSON() + `},"author":` + wireRESTUserJSON() + `,"committer":null,"parents":[],"stats":{"total":0,"additions":0,"deletions":0},"files":[]}`
}
func wireGraphQLJSON() string {
	return `{"data":{"repository":{"object":{"__typename":"Commit","oid":"` + wireOID + `","author":{"name":"bot","email":"bot@example.invalid","date":"2026-01-02T03:04:05Z","user":{"__typename":"User","login":"bot","databaseId":1}},"committer":{"name":"bot","email":"bot@example.invalid","date":"2026-01-02T03:04:05Z","user":null},"signature":{"isValid":true,"state":"VALID","wasSignedByGitHub":true,"signer":{"__typename":"User","login":"bot","databaseId":1}}}}}}`
}

func TestStrictEndpointDecoderParity(t *testing.T) {
	if _, ok := decodeRef([]byte(refJSON("refs/heads/gardener-release-state/minor")), "refs/heads/gardener-release-state/minor", "commit"); !ok {
		t.Fatal("ref")
	}
	if _, ok := decodeRawCommit([]byte(wireRawJSON()), wireOID); !ok {
		t.Fatal("raw")
	}
	if _, ok := decodeRESTCommit([]byte(wireRESTJSON()), wireOID); !ok {
		t.Fatal("rest")
	}
	if _, ok := decodeGQLCommit([]byte(wireGraphQLJSON()), wireOID); !ok {
		t.Fatal("graphql")
	}
	tree := `{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[{"path":"dir/file","mode":"100644","type":"blob","sha":"` + wireOID + `","size":1,"url":"u"},{"path":"dir","mode":"040000","type":"tree","sha":"` + wireParentOID + `","url":"u"}]}`
	// The deliberately nonlexical traversal must remain accepted.
	if v, ok := decodeTree([]byte(tree), wireTreeOID); !ok || len(v.Tree) != 2 {
		t.Fatal("tree")
	}
	raw := []byte("x")
	blobOID := gitBlobOID(raw)
	blob := `{"sha":"` + blobOID + `","node_id":"n","size":1,"url":"u","content":"` + base64.StdEncoding.EncodeToString(raw) + `","encoding":"base64"}`
	if _, ok := decodeBlob([]byte(blob), blobOID, 16); !ok {
		t.Fatal("blob")
	}
	tag := `{"node_id":"n","sha":"` + wireTreeOID + `","url":"u","tag":"v1.2.3","message":"v1.2.3\n","tagger":` + wireIdentityJSON() + `,"object":{"type":"commit","sha":"` + wireOID + `","url":"u"},"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`
	if _, ok := decodeTag([]byte(tag), wireTreeOID, "v1.2.3", wireOID); !ok {
		t.Fatal("tag")
	}
}

func TestStrictEndpointDecodersRejectMalformedSuccess(t *testing.T) {
	cases := []struct {
		name   string
		decode func([]byte) bool
		raw    string
	}{
		{"ref duplicate", func(b []byte) bool {
			_, ok := decodeRef(b, "refs/heads/gardener-release-state/minor", "commit")
			return ok
		}, strings.Replace(refJSON("refs/heads/gardener-release-state/minor"), `"ref":`, `"ref":"x","ref":`, 1)},
		{"raw null", func(b []byte) bool { _, ok := decodeRawCommit(b, wireOID); return ok }, strings.Replace(wireRawJSON(), `"message":"message"`, `"message":null`, 1)},
		{"rest missing", func(b []byte) bool { _, ok := decodeRESTCommit(b, wireOID); return ok }, strings.Replace(wireRESTJSON(), `,"stats":{"total":0,"additions":0,"deletions":0}`, "", 1)},
		{"graphql unknown", func(b []byte) bool { _, ok := decodeGQLCommit(b, wireOID); return ok }, strings.Replace(wireGraphQLJSON(), `"data":`, `"extra":1,"data":`, 1)},
		{"tree bad mode", func(b []byte) bool { _, ok := decodeTree(b, wireTreeOID); return ok }, `{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[{"path":"x","mode":"100755","type":"tree","sha":"` + wireOID + `","url":"u"}]}`},
		{"blob bad encoding", func(b []byte) bool { _, ok := decodeBlob(b, wireOID, 16); return ok }, `{"sha":"` + wireOID + `","node_id":"n","size":1,"url":"u","content":"eA==","encoding":"hex"}`},
		{"tag signed", func(b []byte) bool { _, ok := decodeTag(b, wireTreeOID, "v1.2.3", wireOID); return ok }, `{"node_id":"n","sha":"` + wireTreeOID + `","url":"u","tag":"v1.2.3","message":"v1.2.3\n","tagger":` + wireIdentityJSON() + `,"object":{"type":"commit","sha":"` + wireOID + `","url":"u"},"verification":{"verified":false,"reason":"unsigned","signature":"x","payload":null,"verified_at":null}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.decode([]byte(tc.raw)) {
				t.Fatal("accepted malformed response")
			}
		})
	}
}

func TestStrictIdentityPrincipalAndPathParity(t *testing.T) {
	cases := []struct {
		name   string
		decode func([]byte) bool
		raw    string
	}{
		{"raw email", func(b []byte) bool { _, ok := decodeRawCommit(b, wireOID); return ok }, strings.Replace(wireRawJSON(), `"email":"bot@example.invalid"`, `"email":"not-an-email"`, 1)},
		{"raw name whitespace", func(b []byte) bool { _, ok := decodeRawCommit(b, wireOID); return ok }, strings.Replace(wireRawJSON(), `"name":"bot"`, `"name":" bot"`, 1)},
		{"raw name angle", func(b []byte) bool { _, ok := decodeRawCommit(b, wireOID); return ok }, strings.Replace(wireRawJSON(), `"name":"bot"`, `"name":"bot<admin>"`, 1)},
		{"tagger control", func(b []byte) bool { _, ok := decodeTag(b, wireTreeOID, "v1.2.3", wireOID); return ok }, strings.Replace(`{"node_id":"n","sha":"`+wireTreeOID+`","url":"u","tag":"v1.2.3","message":"v1.2.3\n","tagger":`+wireIdentityJSON()+`,"object":{"type":"commit","sha":"`+wireOID+`","url":"u"},"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,"verified_at":null}}`, `"name":"bot"`, `"name":"bot\nadmin"`, 1)},
		{"rest login", func(b []byte) bool { _, ok := decodeRESTCommit(b, wireOID); return ok }, strings.Replace(wireRESTJSON(), `"login":"bot"`, `"login":"bad/login"`, 1)},
		{"graphql login", func(b []byte) bool { _, ok := decodeGQLCommit(b, wireOID); return ok }, strings.Replace(wireGraphQLJSON(), `"login":"bot"`, `"login":"bad login"`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.decode([]byte(tc.raw)) {
				t.Fatal("accepted invalid identity or principal")
			}
		})
	}
	for _, path := range []string{"../state.json", "/state.json", `dir\\state.json`, "dir/\nstate.json", "dir//state.json", "dir/./state.json", "dir/../state.json", " state.json", "todo/state.json"} {
		raw := `{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[{"path":` + strconv.Quote(path) + `,"mode":"100644","type":"blob","sha":"` + wireOID + `","size":1,"url":"u"}]}`
		if _, ok := decodeTree([]byte(raw), wireTreeOID); ok {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
}

func wireRESTWithFileJSON() string {
	return strings.Replace(wireRESTJSON(), `"files":[]`, `"files":[{"sha":"`+wireOID+`","filename":"dir/file.go","status":"modified","additions":1,"deletions":0,"changes":1,"blob_url":"u","raw_url":"u","contents_url":"u"}]`, 1)
}

func TestRESTFileValidationParity(t *testing.T) {
	valid := wireRESTWithFileJSON()
	if _, ok := decodeRESTCommit([]byte(valid), wireOID); !ok {
		t.Fatal("valid file entry rejected")
	}
	for name, raw := range map[string]string{
		"negative additions": strings.Replace(valid, `"additions":1`, `"additions":-1`, 1),
		"invalid sha":        strings.Replace(valid, `"sha":"`+wireOID+`","filename"`, `"sha":"bad","filename"`, 1),
		"unsafe filename":    strings.Replace(valid, `"filename":"dir/file.go"`, `"filename":"../file.go"`, 1),
		"empty status":       strings.Replace(valid, `"status":"modified"`, `"status":""`, 1),
		"empty blob url":     strings.Replace(valid, `"blob_url":"u"`, `"blob_url":""`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := decodeRESTCommit([]byte(raw), wireOID); ok {
				t.Fatal("accepted malformed REST file entry")
			}
		})
	}
}

func TestCapturedWireShapesStillDecode(t *testing.T) {
	for _, file := range []string{"../../testdata/state_v3_wire/rest_commit_captured.json", "../../testdata/state_v3_wire/graphql_commit_captured.json", "../../testdata/state_v3_wire/tree_recursive_captured.json"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(file, "rest_"):
			if _, ok := decodeRESTCommit(raw, "cad99b7e5a9b4b7566f1d909a5b5e2262103ef5c"); !ok {
				t.Fatal(file)
			}
		case strings.Contains(file, "graphql_"):
			if _, ok := decodeGQLCommit(raw, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !ok {
				t.Fatal(file)
			}
		case strings.Contains(file, "tree_"):
			if _, ok := decodeTree(raw, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); !ok {
				t.Fatal(file)
			}
		}
	}
}
