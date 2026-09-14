// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	stateV3CreateCommitQuery = "mutation CreateCommitOnBranch($input:CreateCommitOnBranchInput!){createCommitOnBranch(input:$input){commit{oid}}}"
	stateV3CommitQuery       = "query StateV3Commit($owner:String!,$name:String!,$oid:GitObjectID!){repository(owner:$owner,name:$name){object(oid:$oid){__typename ... on Commit{oid author{name email date user{__typename login databaseId}} committer{name email date user{__typename login databaseId}} signature{isValid state wasSignedByGitHub signer{__typename login databaseId}}}}}}"
)

// stateV3PreparedCommitPermit is opaque state-commit authority. It is created
// only from an authenticated current record whose final durable arm is the
// exact platform-commit arm. This package deliberately has no executor.
type stateV3PreparedCommitPermit struct {
	raw            []byte
	record         StateV3Record
	policy         StateV3Policy
	authentication StateV3Authentication
	envelope       StateV3StagedEnvelopeEvidence
	stateRef       string
	expectedOID    string
}

// stateV3PreparedCommitPermitForRecord derives the only supported state-write
// request permit from the exact canonical record bytes and independently
// authenticated state snapshot. It deliberately does not treat package-private
// data as an authorization boundary: serialization validates these inputs again.
func stateV3PreparedCommitPermitForRecord(raw []byte, record StateV3Record, policy StateV3Policy, authentication StateV3Authentication, envelope StateV3StagedEnvelopeEvidence) (stateV3PreparedCommitPermit, error) {
	if ValidateStateV3Record(raw, record, policy, authentication) != nil || record.Prepared == nil || record.Phase != StateV3PhasePrepared || record.Commit != nil || len(record.Events) == 0 || record.Events[len(record.Events)-1].Kind != StateV3EventMutationArmed || len(record.MutationArms) == 0 || !validStateV3StagedEnvelope(envelope, record.Reservation, policy) {
		return stateV3PreparedCommitPermit{}, typedContractError("invalid_state_v3_prepared_commit_permit")
	}
	arm := record.MutationArms[len(record.MutationArms)-1]
	expected, ok := stateV3ExpectedMutationArm(record, "platform_commit", record.Phase, false, false, len(record.BranchEvidence), 0, 0)
	if !ok || arm != expected || arm.ExpectedOldMissing || arm.ExpectedOldOID != record.Prepared.Mutation.ExpectedHeadOID || arm.Ref != record.Prepared.Mutation.TargetRef || arm.IntendedObjectKnown || arm.Attempt != 1 || record.Events[len(record.Events)-1].Ref != arm.Ref || record.Events[len(record.Events)-1].ExpectedOldOID != arm.ExpectedOldOID {
		return stateV3PreparedCommitPermit{}, typedContractError("invalid_state_v3_prepared_commit_permit")
	}
	lane, ok := stateV3LaneForReservation(record.Reservation, policy)
	if !ok {
		return stateV3PreparedCommitPermit{}, typedContractError("invalid_state_v3_prepared_commit_permit")
	}
	return stateV3PreparedCommitPermit{raw: append([]byte(nil), raw...), record: record, policy: policy, authentication: authentication, envelope: envelope, stateRef: lane.StateRef, expectedOID: arm.ExpectedOldOID}, nil
}

// stateV3PreparedCreateCommitRequest contains canonical bytes derived solely
// from a durable-arm-bound permit. Its accessor returns a defensive copy.
type stateV3PreparedCreateCommitRequest struct {
	body []byte
}

func (request stateV3PreparedCreateCommitRequest) bytes() []byte {
	return append([]byte(nil), request.body...)
}

func stateV3BuildPreparedCreateCommitRequest(permit stateV3PreparedCommitPermit) (stateV3PreparedCreateCommitRequest, error) {
	// Revalidate the entire authenticated arm binding. A package-local struct is
	// constructible by future package code and must never be trusted by itself.
	validated, err := stateV3PreparedCommitPermitForRecord(permit.raw, permit.record, permit.policy, permit.authentication, permit.envelope)
	if err != nil || validated.stateRef != permit.stateRef || validated.expectedOID != permit.expectedOID {
		return stateV3PreparedCreateCommitRequest{}, typedContractError("invalid_state_v3_prepared_commit_permit")
	}
	permit = validated
	additions := make([]stateV3WireAddition, len(permit.envelope.Files))
	for index, addition := range permit.envelope.Files {
		additions[index] = stateV3WireAddition{Path: addition.Path, Contents: base64.StdEncoding.EncodeToString(addition.Raw)}
	}
	request := struct {
		Query     string `json:"query"`
		Variables struct {
			Input struct {
				Branch struct {
					RepositoryNameWithOwner string `json:"repositoryNameWithOwner"`
					BranchName              string `json:"branchName"`
				} `json:"branch"`
				ExpectedHeadOID string `json:"expectedHeadOid"`
				Message         struct {
					Headline string `json:"headline"`
				} `json:"message"`
				FileChanges struct {
					Additions []stateV3WireAddition `json:"additions"`
				} `json:"fileChanges"`
			} `json:"input"`
		} `json:"variables"`
	}{Query: stateV3CreateCommitQuery}
	request.Variables.Input.Branch.RepositoryNameWithOwner = RepositoryFullName
	request.Variables.Input.Branch.BranchName = strings.TrimPrefix(permit.stateRef, "refs/heads/")
	request.Variables.Input.ExpectedHeadOID = permit.expectedOID
	request.Variables.Input.Message.Headline = "gardener-release state: " + permit.record.Reservation.RequestKey
	request.Variables.Input.FileChanges.Additions = additions
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > MaxStateV3GraphQLRequestBytes {
		return stateV3PreparedCreateCommitRequest{}, typedContractError("state_v3_create_commit_request_too_large")
	}
	return stateV3PreparedCreateCommitRequest{body: raw}, nil
}

type stateV3WireAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// stateV3ReadRequest contains only a fixed relative GitHub API endpoint. No
// host, scheme, credentials, or HTTP operation is represented in this layer.
type stateV3ReadRequest struct {
	method string
	path   string
	body   []byte
}

func stateV3RESTRefRead(ref string) (stateV3ReadRequest, error) {
	if ref != StateV3MinorStateRef && ref != StateV3PatchStateRef && ref != StateV3CoordinationRef {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_read_ref")
	}
	return stateV3ReadRequest{method: "GET", path: "/repos/" + RepositoryFullName + "/git/ref/" + strings.TrimPrefix(ref, "refs/")}, nil
}

// stateV3BranchRefRead derives a release branch read only from the exact
// persisted branch intent in a semantically valid record.
func stateV3BranchRefRead(record StateV3Record, policy StateV3Policy, index int) (stateV3ReadRequest, error) {
	if validateStateV3RecordCore(record, policy) != nil || record.Prepared == nil || index < 0 || index >= len(record.Prepared.Mutation.Branches) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_branch_read")
	}
	intent := record.Prepared.Mutation.Branches[index]
	if !matchesStateV3BranchIntents(record.Prepared.Mutation.Branches, record.Reservation) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_branch_read")
	}
	return stateV3ReadRequest{method: "GET", path: "/repos/" + RepositoryFullName + "/git/ref/" + strings.TrimPrefix(intent.Ref, "refs/")}, nil
}

// stateV3TagRefRead derives an immutable tag-ref read only from an exact
// persisted tag intent in a semantically valid record.
func stateV3TagRefRead(record StateV3Record, policy StateV3Policy, index int) (stateV3ReadRequest, error) {
	if validateStateV3RecordCore(record, policy) != nil || record.Commit == nil || index < 0 || index >= len(record.TagIntents) || !validStateV3TagIntents(record.TagIntents, record.TagPlans, *record.Commit) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_tag_read")
	}
	return stateV3ReadRequest{method: "GET", path: "/repos/" + RepositoryFullName + "/git/ref/" + strings.TrimPrefix(record.TagIntents[index].Ref, "refs/")}, nil
}
func stateV3RawCommitRead(oid string) (stateV3ReadRequest, error) {
	return stateV3GitObjectRead("commits", oid)
}
func stateV3RESTCommitRead(oid string) (stateV3ReadRequest, error) {
	if !validStateV3OID(oid) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_read_oid")
	}
	return stateV3ReadRequest{method: "GET", path: "/repos/" + RepositoryFullName + "/commits/" + oid}, nil
}
func stateV3TreeRead(oid string) (stateV3ReadRequest, error) {
	request, err := stateV3GitObjectRead("trees", oid)
	if err != nil {
		return stateV3ReadRequest{}, err
	}
	request.path += "?recursive=1"
	return request, nil
}
func stateV3BlobRead(oid string) (stateV3ReadRequest, error) {
	return stateV3GitObjectRead("blobs", oid)
}
func stateV3AnnotatedTagRead(oid string) (stateV3ReadRequest, error) {
	return stateV3GitObjectRead("tags", oid)
}
func stateV3GitObjectRead(kind, oid string) (stateV3ReadRequest, error) {
	if !validStateV3OID(oid) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_read_oid")
	}
	return stateV3ReadRequest{method: "GET", path: "/repos/" + RepositoryFullName + "/git/" + kind + "/" + oid}, nil
}
func stateV3GraphQLCommitRead(oid string) (stateV3ReadRequest, error) {
	if !validStateV3OID(oid) {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_read_oid")
	}
	request := struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}{
		Query: stateV3CommitQuery, Variables: map[string]string{"owner": "DataDog", "name": "dd-trace-go", "oid": oid},
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > MaxStateV3GraphQLRequestBytes {
		return stateV3ReadRequest{}, typedContractError("invalid_state_v3_read_request")
	}
	return stateV3ReadRequest{method: "POST", path: "/graphql", body: raw}, nil
}

// StateV3WireRef is the complete reviewed REST git-ref document.
type StateV3WireRef struct {
	Ref    string `json:"ref"`
	NodeID string `json:"node_id"`
	URL    string `json:"url"`
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"object"`
}

func decodeStateV3WireRef(raw []byte, expectedRef, expectedType string) (StateV3WireRef, error) {
	var value StateV3WireRef
	if err := decodeStateV3WireDocument(raw, &value); err != nil || !stateV3WireObjectHasFields(raw, nil, "ref", "node_id", "url", "object") || !stateV3WireObjectHasFields(raw, []string{"object"}, "sha", "type", "url") || value.Ref != expectedRef || value.Object.Type != expectedType || !validStateV3OID(value.Object.SHA) || !validWireMetadata(value.NodeID, value.URL, value.Object.URL) {
		return StateV3WireRef{}, typedEvidenceError("invalid_state_v3_ref_response")
	}
	return value, nil
}

type stateV3WireGitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}
type stateV3WireGitTree struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}
type stateV3WireGitParent struct {
	SHA     string `json:"sha"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}
type stateV3WireVerification struct {
	Verified   *bool   `json:"verified"`
	Reason     string  `json:"reason"`
	Signature  *string `json:"signature"`
	Payload    *string `json:"payload"`
	VerifiedAt *string `json:"verified_at"`
}

// StateV3WireCommit is the complete raw Git-commit response projection.
type StateV3WireCommit struct {
	SHA          string                  `json:"sha"`
	NodeID       string                  `json:"node_id"`
	URL          string                  `json:"url"`
	HTMLURL      string                  `json:"html_url"`
	Author       stateV3WireGitIdentity  `json:"author"`
	Committer    stateV3WireGitIdentity  `json:"committer"`
	Tree         stateV3WireGitTree      `json:"tree"`
	Message      string                  `json:"message"`
	Parents      []stateV3WireGitParent  `json:"parents"`
	Verification stateV3WireVerification `json:"verification"`
}

func decodeStateV3WireCommit(raw []byte, expectedOID string) (StateV3WireCommit, error) {
	var value StateV3WireCommit
	if err := decodeStateV3WireDocument(raw, &value); err != nil || !stateV3WireObjectHasFields(raw, nil, "sha", "node_id", "url", "html_url", "author", "committer", "tree", "message", "parents", "verification") || !stateV3WireObjectHasFields(raw, []string{"author"}, "name", "email", "date") || !stateV3WireObjectHasFields(raw, []string{"committer"}, "name", "email", "date") || !stateV3WireObjectHasFields(raw, []string{"tree"}, "sha", "url") || !stateV3WireArrayObjectHasFields(raw, []string{"parents"}, "sha", "url", "html_url") || !stateV3WireVerificationFieldsPresent(raw, []string{"verification"}) || value.SHA != expectedOID || !validStateV3OID(value.Tree.SHA) || len(value.Parents) > 1 || !validWireGitIdentity(value.Author) || !validWireGitIdentity(value.Committer) || !validStateV3Text(value.Message, 8*1024) || !validWireMetadata(value.NodeID, value.URL, value.HTMLURL, value.Tree.URL) || !validWireVerification(value.Verification) {
		return StateV3WireCommit{}, typedEvidenceError("invalid_state_v3_commit_response")
	}
	for _, parent := range value.Parents {
		if !validStateV3OID(parent.SHA) || !validWireMetadata(parent.URL, parent.HTMLURL) {
			return StateV3WireCommit{}, typedEvidenceError("invalid_state_v3_commit_response")
		}
	}
	return value, nil
}

// StateV3WireRESTCommit retains REST identities separately from raw and
// GraphQL evidence so later semantic validation can compare all three. Its
// paginated files list is role evidence only, never a complete tree delta.
type StateV3WireRESTCommit struct {
	SHA         string `json:"sha"`
	NodeID      string `json:"node_id"`
	URL         string `json:"url"`
	HTMLURL     string `json:"html_url"`
	CommentsURL string `json:"comments_url"`
	Commit      struct {
		Author       stateV3WireGitIdentity  `json:"author"`
		Committer    stateV3WireGitIdentity  `json:"committer"`
		Message      string                  `json:"message"`
		Tree         stateV3WireGitTree      `json:"tree"`
		URL          string                  `json:"url"`
		CommentCount int                     `json:"comment_count"`
		Verification stateV3WireVerification `json:"verification"`
	} `json:"commit"`
	Author    *stateV3WireRESTIdentity `json:"author"`
	Committer *stateV3WireRESTIdentity `json:"committer"`
	Parents   []stateV3WireGitParent   `json:"parents"`
	Stats     struct {
		Total     int `json:"total"`
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
	} `json:"stats"`
	Files []struct {
		SHA              string  `json:"sha"`
		Filename         string  `json:"filename"`
		Status           string  `json:"status"`
		Additions        int     `json:"additions"`
		Deletions        int     `json:"deletions"`
		Changes          int     `json:"changes"`
		BlobURL          string  `json:"blob_url"`
		RawURL           string  `json:"raw_url"`
		ContentsURL      string  `json:"contents_url"`
		Patch            *string `json:"patch,omitempty"`
		PreviousFilename *string `json:"previous_filename,omitempty"`
	} `json:"files"`
}
type stateV3WireRESTIdentity struct {
	Login             string `json:"login"`
	ID                int64  `json:"id"`
	NodeID            string `json:"node_id"`
	AvatarURL         string `json:"avatar_url"`
	GravatarID        string `json:"gravatar_id"`
	HTMLURL           string `json:"html_url"`
	URL               string `json:"url"`
	FollowersURL      string `json:"followers_url"`
	FollowingURL      string `json:"following_url"`
	GistsURL          string `json:"gists_url"`
	StarredURL        string `json:"starred_url"`
	SubscriptionsURL  string `json:"subscriptions_url"`
	OrganizationsURL  string `json:"organizations_url"`
	ReposURL          string `json:"repos_url"`
	EventsURL         string `json:"events_url"`
	ReceivedEventsURL string `json:"received_events_url"`
	Type              string `json:"type"`
	UserViewType      string `json:"user_view_type"`
	SiteAdmin         bool   `json:"site_admin"`
}

func decodeStateV3WireRESTCommit(raw []byte, expectedOID string) (StateV3WireRESTCommit, error) {
	var value StateV3WireRESTCommit
	if err := decodeStateV3WireDocumentAllowNull(raw, &value, "author", "committer"); err != nil || !stateV3WireObjectHasFields(raw, nil, "sha", "node_id", "url", "html_url", "comments_url", "commit", "author", "committer", "parents", "stats", "files") || !stateV3WireObjectHasFields(raw, []string{"commit"}, "author", "committer", "message", "tree", "url", "comment_count", "verification") || !stateV3WireObjectHasFields(raw, []string{"commit", "author"}, "name", "email", "date") || !stateV3WireObjectHasFields(raw, []string{"commit", "committer"}, "name", "email", "date") || !stateV3WireObjectHasFields(raw, []string{"commit", "tree"}, "sha", "url") || !stateV3WireVerificationFieldsPresent(raw, []string{"commit", "verification"}) || !stateV3WireObjectHasFields(raw, []string{"stats"}, "total", "additions", "deletions") || !stateV3WireArrayObjectHasFields(raw, []string{"parents"}, "sha", "url", "html_url") || !stateV3WireArrayObjectHasFields(raw, []string{"files"}, "sha", "filename", "status", "additions", "deletions", "changes", "blob_url", "raw_url", "contents_url") || !stateV3WireRESTIdentityFieldsPresent(raw, "author", value.Author) || !stateV3WireRESTIdentityFieldsPresent(raw, "committer", value.Committer) || value.SHA != expectedOID || !validWireMetadata(value.NodeID, value.URL, value.HTMLURL, value.CommentsURL, value.Commit.Tree.URL, value.Commit.URL) || value.Commit.CommentCount < 0 || value.Stats.Total < 0 || value.Stats.Additions < 0 || value.Stats.Deletions < 0 || !validWireGitIdentity(value.Commit.Author) || !validWireGitIdentity(value.Commit.Committer) || !validStateV3Text(value.Commit.Message, 8*1024) || !validStateV3OID(value.Commit.Tree.SHA) || !validWireVerification(value.Commit.Verification) || !validWireRESTOptionalIdentity(value.Author) || !validWireRESTOptionalIdentity(value.Committer) || !validStateV3WireRESTFiles(value.Files) {
		return StateV3WireRESTCommit{}, typedEvidenceError("invalid_state_v3_rest_commit_response")
	}
	return value, nil
}

func (value StateV3WireRESTCommit) roles() (StateV3RawIdentity, StateV3AssociatedIdentity, StateV3OptionalAssociatedIdentity, bool) {
	author, ok := stateV3WireRESTIdentityValue(value.Author, false)
	if !ok {
		return StateV3RawIdentity{}, StateV3AssociatedIdentity{}, StateV3OptionalAssociatedIdentity{}, false
	}
	committer, ok := stateV3WireRESTIdentityValue(value.Committer, true)
	if !ok {
		return StateV3RawIdentity{}, StateV3AssociatedIdentity{}, StateV3OptionalAssociatedIdentity{}, false
	}
	return StateV3RawIdentity{Name: value.Commit.Author.Name, Email: value.Commit.Author.Email}, author.Identity, committer, true
}

type stateV3WireTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size,omitempty"`
	URL  string `json:"url"`
}
type StateV3WireTree struct {
	SHA       string                 `json:"sha"`
	URL       string                 `json:"url"`
	Truncated bool                   `json:"truncated"`
	Tree      []stateV3WireTreeEntry `json:"tree"`
}

func decodeStateV3WireTree(raw []byte, expectedOID string) (StateV3WireTree, error) {
	var value StateV3WireTree
	if err := decodeStateV3WireDocument(raw, &value); err != nil || value.SHA != expectedOID || !stateV3WireObjectHasFields(raw, nil, "sha", "url", "truncated", "tree") || value.Truncated || len(value.Tree) == 0 || len(value.Tree) > 10_000 || value.URL == "" {
		return StateV3WireTree{}, typedEvidenceError("invalid_state_v3_tree_response")
	}
	paths := make(map[string]struct{}, len(value.Tree))
	for _, entry := range value.Tree {
		if !validRepositoryRelativePath(entry.Path) || !validStateV3OID(entry.SHA) || entry.URL == "" {
			return StateV3WireTree{}, typedEvidenceError("invalid_state_v3_tree_response")
		}
		if _, exists := paths[entry.Path]; exists {
			return StateV3WireTree{}, typedEvidenceError("invalid_state_v3_tree_response")
		}
		paths[entry.Path] = struct{}{}
		switch {
		case entry.Type == "tree" && entry.Mode == "040000" && entry.Size == nil:
		case entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755" || entry.Mode == "120000") && entry.Size != nil && *entry.Size >= 0:
		case entry.Type == "commit" && entry.Mode == "160000" && entry.Size == nil:
		default:
			return StateV3WireTree{}, typedEvidenceError("invalid_state_v3_tree_response")
		}
	}
	return value, nil
}

// regularFileEntries deliberately projects the complete recursive collection
// into the narrower persisted state-tree form. Directory entries are expected;
// non-regular leaves fail closed rather than being normalized away.
func (value StateV3WireTree) regularFileEntries() ([]StateV3StateTreeEntry, bool) {
	result := make([]StateV3StateTreeEntry, 0, len(value.Tree))
	for _, entry := range value.Tree {
		if entry.Type == "tree" {
			continue
		}
		if entry.Type != "blob" || entry.Mode != "100644" || entry.Size == nil {
			return nil, false
		}
		result = append(result, StateV3StateTreeEntry{Path: entry.Path, Mode: entry.Mode, Type: entry.Type, OID: entry.SHA})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, true
}

type StateV3WireBlob struct {
	SHA      string `json:"sha"`
	NodeID   string `json:"node_id"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Raw      []byte `json:"-"`
}

func decodeStateV3WireBlob(raw []byte, expectedOID string, maxDecoded int) (StateV3WireBlob, error) {
	var value StateV3WireBlob
	if err := decodeStateV3WireDocument(raw, &value); err != nil || !stateV3WireObjectHasFields(raw, nil, "sha", "node_id", "size", "url", "content", "encoding") || value.SHA != expectedOID || value.Encoding != "base64" || value.Size < 1 || value.URL == "" || value.NodeID == "" || maxDecoded <= 0 {
		return StateV3WireBlob{}, typedEvidenceError("invalid_state_v3_blob_response")
	}
	decoded, err := stateV3DecodeGitHubBase64(value.Content, maxDecoded)
	if err != nil || int64(len(decoded)) != value.Size || stateV3GitBlobOID(decoded) != expectedOID {
		return StateV3WireBlob{}, typedEvidenceError("invalid_state_v3_blob_response")
	}
	value.Raw = decoded
	return value, nil
}

type StateV3WireAnnotatedTag struct {
	NodeID  string                 `json:"node_id"`
	SHA     string                 `json:"sha"`
	URL     string                 `json:"url"`
	Tag     string                 `json:"tag"`
	Message string                 `json:"message"`
	Tagger  stateV3WireGitIdentity `json:"tagger"`
	Object  struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
		URL  string `json:"url"`
	} `json:"object"`
	Verification stateV3WireVerification `json:"verification"`
}

func decodeStateV3WireAnnotatedTag(raw []byte, expectedOID, expectedName, expectedCommit string) (StateV3WireAnnotatedTag, error) {
	var value StateV3WireAnnotatedTag
	if err := decodeStateV3WireDocumentAllowNull(raw, &value, "verification.signature", "verification.payload", "verification.verified_at"); err != nil || !stateV3WireObjectHasFields(raw, nil, "node_id", "sha", "url", "tag", "message", "tagger", "object", "verification") || !stateV3WireObjectHasFields(raw, []string{"tagger"}, "name", "email", "date") || !stateV3WireObjectHasFields(raw, []string{"object"}, "type", "sha", "url") || value.SHA != expectedOID || value.Tag != expectedName || value.Message != expectedName+"\n" || value.Object.Type != "commit" || value.Object.SHA != expectedCommit || !validWireMetadata(value.NodeID, value.URL, value.Object.URL) || !validWireGitIdentity(value.Tagger) || !stateV3WireObjectHasFields(raw, []string{"verification"}, "verified", "reason", "signature", "payload", "verified_at") || !validWireUnsignedTagVerification(value.Verification) {
		return StateV3WireAnnotatedTag{}, typedEvidenceError("invalid_state_v3_tag_response")
	}
	return value, nil
}

type stateV3WireGraphQLIdentity struct {
	Typename   string `json:"__typename"`
	Login      string `json:"login"`
	DatabaseID int64  `json:"databaseId"`
}
type stateV3WireGraphQLActor struct {
	Name  string                      `json:"name"`
	Email string                      `json:"email"`
	Date  string                      `json:"date"`
	User  *stateV3WireGraphQLIdentity `json:"user"`
}
type StateV3WireGraphQLCommit struct {
	Data struct {
		Repository struct {
			Object struct {
				Typename  string                  `json:"__typename"`
				OID       string                  `json:"oid"`
				Author    stateV3WireGraphQLActor `json:"author"`
				Committer stateV3WireGraphQLActor `json:"committer"`
				Signature struct {
					IsValid           *bool                       `json:"isValid"`
					State             string                      `json:"state"`
					WasSignedByGitHub *bool                       `json:"wasSignedByGitHub"`
					Signer            *stateV3WireGraphQLIdentity `json:"signer"`
				} `json:"signature"`
			} `json:"object"`
		} `json:"repository"`
	} `json:"data"`
}

func decodeStateV3WireGraphQLCommit(raw []byte, expectedOID string) (StateV3WireGraphQLCommit, error) {
	var value StateV3WireGraphQLCommit
	if err := decodeStateV3WireDocumentAllowNull(raw, &value, "data.repository.object.author.user", "data.repository.object.committer.user"); err != nil || !stateV3WireObjectHasFields(raw, nil, "data") || !stateV3WireObjectHasFields(raw, []string{"data"}, "repository") || !stateV3WireObjectHasFields(raw, []string{"data", "repository"}, "object") || !stateV3WireObjectHasFields(raw, []string{"data", "repository", "object"}, "__typename", "oid", "author", "committer", "signature") || !stateV3WireObjectHasFields(raw, []string{"data", "repository", "object", "author"}, "name", "email", "date", "user") || !stateV3WireObjectHasFields(raw, []string{"data", "repository", "object", "committer"}, "name", "email", "date", "user") || !stateV3WireObjectHasFields(raw, []string{"data", "repository", "object", "signature"}, "isValid", "state", "wasSignedByGitHub", "signer") || !stateV3WireGraphQLIdentityFieldsPresent(raw, []string{"data", "repository", "object", "author", "user"}, value.Data.Repository.Object.Author.User) || !stateV3WireGraphQLIdentityFieldsPresent(raw, []string{"data", "repository", "object", "committer", "user"}, value.Data.Repository.Object.Committer.User) || !stateV3WireGraphQLIdentityFieldsPresent(raw, []string{"data", "repository", "object", "signature", "signer"}, value.Data.Repository.Object.Signature.Signer) || value.Data.Repository.Object.Typename != "Commit" || value.Data.Repository.Object.OID != expectedOID || !validWireGraphQLActor(value.Data.Repository.Object.Author, false) || !validWireGraphQLActor(value.Data.Repository.Object.Committer, true) || value.Data.Repository.Object.Signature.IsValid == nil || !*value.Data.Repository.Object.Signature.IsValid || value.Data.Repository.Object.Signature.State != StateV3RequiredSignatureState || value.Data.Repository.Object.Signature.WasSignedByGitHub == nil || !*value.Data.Repository.Object.Signature.WasSignedByGitHub || !validWireGraphQLIdentity(value.Data.Repository.Object.Signature.Signer) {
		return StateV3WireGraphQLCommit{}, typedEvidenceError("invalid_state_v3_graphql_commit_response")
	}
	return value, nil
}
func (value StateV3WireGraphQLCommit) roles() (StateV3AssociatedIdentity, StateV3OptionalAssociatedIdentity, StateV3AssociatedIdentity, bool) {
	author, ok := stateV3WireGraphQLIdentityValue(value.Data.Repository.Object.Author.User, false)
	if !ok {
		return StateV3AssociatedIdentity{}, StateV3OptionalAssociatedIdentity{}, StateV3AssociatedIdentity{}, false
	}
	committer, ok := stateV3WireGraphQLIdentityValue(value.Data.Repository.Object.Committer.User, true)
	if !ok {
		return StateV3AssociatedIdentity{}, StateV3OptionalAssociatedIdentity{}, StateV3AssociatedIdentity{}, false
	}
	signer, ok := stateV3WireGraphQLIdentityValue(value.Data.Repository.Object.Signature.Signer, false)
	if !ok {
		return StateV3AssociatedIdentity{}, StateV3OptionalAssociatedIdentity{}, StateV3AssociatedIdentity{}, false
	}
	return author.Identity, committer, signer.Identity, true
}

func decodeStateV3WireCreateCommitResponse(raw []byte, expectedOID string) error {
	var value struct {
		Data struct {
			CreateCommitOnBranch struct {
				Commit struct {
					OID string `json:"oid"`
				} `json:"commit"`
			} `json:"createCommitOnBranch"`
		} `json:"data"`
	}
	if !validStateV3OID(expectedOID) || decodeStateV3WireDocument(raw, &value) != nil || !stateV3WireObjectHasFields(raw, nil, "data") || !stateV3WireObjectHasFields(raw, []string{"data"}, "createCommitOnBranch") || !stateV3WireObjectHasFields(raw, []string{"data", "createCommitOnBranch"}, "commit") || !stateV3WireObjectHasFields(raw, []string{"data", "createCommitOnBranch", "commit"}, "oid") || !validStateV3OID(value.Data.CreateCommitOnBranch.Commit.OID) || value.Data.CreateCommitOnBranch.Commit.OID != expectedOID {
		return typedEvidenceError("invalid_state_v3_create_commit_response")
	}
	return nil
}

// decodeStateV3WireDocument rejects duplicate keys, unknown fields, trailing
// data, and every JSON null. The narrow nullable variant is used only for
// reviewed association and unsigned-tag verification fields.
func decodeStateV3WireDocument(raw []byte, target any) error {
	return decodeStateV3WireDocumentAllowNull(raw, target)
}
func decodeStateV3WireDocumentAllowNull(raw []byte, target any, nullablePaths ...string) error {
	if len(raw) == 0 || len(raw) > MaxGitHubResponseBytes || stateV3ValidateWireJSON(raw, nullablePaths...) != nil {
		return typedEvidenceError("invalid_state_v3_wire_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return typedEvidenceError("invalid_state_v3_wire_json")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return typedEvidenceError("invalid_state_v3_wire_json")
	}
	return nil
}
func stateV3ValidateWireJSON(raw []byte, nullablePaths ...string) error {
	allowed := map[string]bool{}
	for _, path := range nullablePaths {
		allowed[path] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := stateV3ScanWireJSONValue(decoder, nil, allowed); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return errors.New("trailing_json")
	}
	return nil
}
func stateV3ScanWireJSONValue(decoder *json.Decoder, path []string, allowed map[string]bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if !allowed[strings.Join(path, ".")] {
			return errors.New("unexpected_json_null")
		}
		return nil
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate_json_key")
			}
			seen[name] = true
			if err := stateV3ScanWireJSONValue(decoder, append(path, name), allowed); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid_json_object")
		}
	case '[':
		for decoder.More() {
			if err := stateV3ScanWireJSONValue(decoder, append(path, "[]"), allowed); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid_json_array")
		}
	default:
		return errors.New("invalid_json_delimiter")
	}
	return nil
}

// stateV3WireObjectHasFields distinguishes omitted required fields from
// present JSON null, which is separately controlled by the strict scanner.
func stateV3WireObjectHasFields(raw []byte, path []string, fields ...string) bool {
	var current map[string]json.RawMessage
	if json.Unmarshal(raw, &current) != nil {
		return false
	}
	for _, segment := range path {
		child, ok := current[segment]
		if !ok {
			return false
		}
		if json.Unmarshal(child, &current) != nil {
			return false
		}
	}
	for _, field := range fields {
		if _, ok := current[field]; !ok {
			return false
		}
	}
	return true
}

func stateV3WireArrayObjectHasFields(raw []byte, path []string, fields ...string) bool {
	var current map[string]json.RawMessage
	if json.Unmarshal(raw, &current) != nil {
		return false
	}
	for _, segment := range path[:len(path)-1] {
		child, ok := current[segment]
		if !ok || json.Unmarshal(child, &current) != nil {
			return false
		}
	}
	arrayRaw, ok := current[path[len(path)-1]]
	if !ok {
		return false
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(arrayRaw, &items) != nil {
		return false
	}
	for _, item := range items {
		for _, field := range fields {
			if _, ok := item[field]; !ok {
				return false
			}
		}
	}
	return true
}

func stateV3WireVerificationFieldsPresent(raw []byte, path []string) bool {
	return stateV3WireObjectHasFields(raw, path, "verified", "reason", "signature", "payload", "verified_at")
}

func stateV3WireGraphQLIdentityFieldsPresent(raw []byte, path []string, identity *stateV3WireGraphQLIdentity) bool {
	if identity == nil {
		return true
	}
	return stateV3WireObjectHasFields(raw, path, "__typename", "login", "databaseId")
}

func stateV3WireRESTIdentityFieldsPresent(raw []byte, root string, identity *stateV3WireRESTIdentity) bool {
	if identity == nil {
		return true
	}
	return stateV3WireObjectHasFields(raw, []string{root}, "login", "id", "node_id", "avatar_url", "gravatar_id", "html_url", "url", "followers_url", "following_url", "gists_url", "starred_url", "subscriptions_url", "organizations_url", "repos_url", "events_url", "received_events_url", "type", "user_view_type", "site_admin")
}

func validStateV3WireRESTFiles(files []struct {
	SHA              string  `json:"sha"`
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
	Changes          int     `json:"changes"`
	BlobURL          string  `json:"blob_url"`
	RawURL           string  `json:"raw_url"`
	ContentsURL      string  `json:"contents_url"`
	Patch            *string `json:"patch,omitempty"`
	PreviousFilename *string `json:"previous_filename,omitempty"`
}) bool {
	for _, file := range files {
		if !validStateV3OID(file.SHA) || !validRepositoryRelativePath(file.Filename) || file.Status == "" || file.Additions < 0 || file.Deletions < 0 || file.Changes < 0 || !validWireMetadata(file.BlobURL, file.RawURL, file.ContentsURL) {
			return false
		}
	}
	return true
}

func validWireMetadata(values ...string) bool {
	for _, value := range values {
		if value == "" {
			return false
		}
	}
	return true
}
func validWireGitIdentity(value stateV3WireGitIdentity) bool {
	return validStateV3RawIdentity(StateV3RawIdentity{Name: value.Name, Email: value.Email}) && validWireDate(value.Date)
}
func validWireDate(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.Format(time.RFC3339) == value
}
func validWireVerification(value stateV3WireVerification) bool {
	return value.Verified != nil && *value.Verified && validStateV3Text(value.Reason, 128) && value.Signature != nil && *value.Signature != "" && value.Payload != nil && *value.Payload != "" && value.VerifiedAt != nil && validWireDate(*value.VerifiedAt)
}
func validWireUnsignedTagVerification(value stateV3WireVerification) bool {
	return value.Verified != nil && !*value.Verified && value.Reason == "unsigned" && value.Signature == nil && value.Payload == nil && value.VerifiedAt == nil
}
func validWireRESTOptionalIdentity(value *stateV3WireRESTIdentity) bool {
	_, ok := stateV3WireRESTIdentityValue(value, true)
	return ok
}
func stateV3WireRESTIdentityValue(value *stateV3WireRESTIdentity, optional bool) (StateV3OptionalAssociatedIdentity, bool) {
	if value == nil {
		return StateV3OptionalAssociatedIdentity{Present: false}, optional
	}
	if value.ID <= 0 || value.Login == "" || (value.Type != "User" && value.Type != "Bot") || value.UserViewType == "" || !validWireMetadata(value.NodeID, value.AvatarURL, value.HTMLURL, value.URL, value.FollowersURL, value.FollowingURL, value.GistsURL, value.StarredURL, value.SubscriptionsURL, value.OrganizationsURL, value.ReposURL, value.EventsURL, value.ReceivedEventsURL) {
		return StateV3OptionalAssociatedIdentity{}, false
	}
	identity := StateV3AssociatedIdentity{Login: value.Login, DatabaseID: strconv.FormatInt(value.ID, 10), Type: value.Type}
	return StateV3OptionalAssociatedIdentity{Present: true, Identity: identity}, validStateV3AssociatedIdentity(identity)
}
func validWireGraphQLIdentity(value *stateV3WireGraphQLIdentity) bool {
	_, ok := stateV3WireGraphQLIdentityValue(value, false)
	return ok
}
func validWireGraphQLActor(value stateV3WireGraphQLActor, optional bool) bool {
	return validWireGitIdentity(stateV3WireGitIdentity{Name: value.Name, Email: value.Email, Date: value.Date}) && func() bool { _, ok := stateV3WireGraphQLIdentityValue(value.User, optional); return ok }()
}
func stateV3WireGraphQLIdentityValue(value *stateV3WireGraphQLIdentity, optional bool) (StateV3OptionalAssociatedIdentity, bool) {
	if value == nil {
		return StateV3OptionalAssociatedIdentity{Present: false}, optional
	}
	if value.Typename != "User" && value.Typename != "Bot" {
		return StateV3OptionalAssociatedIdentity{}, false
	}
	identity := StateV3AssociatedIdentity{Login: value.Login, DatabaseID: strconv.FormatInt(value.DatabaseID, 10), Type: value.Typename}
	return StateV3OptionalAssociatedIdentity{Present: true, Identity: identity}, validStateV3AssociatedIdentity(identity)
}

func stateV3DecodeGitHubBase64(value string, maximum int) ([]byte, error) {
	if value == "" || strings.ContainsAny(value, "\r\t ") {
		return nil, errors.New("invalid_base64")
	}
	lines := strings.Split(value, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for index, line := range lines {
		if line == "" || (index < len(lines)-1 && len(line) != 76) || len(line) > 76 {
			return nil, errors.New("invalid_base64")
		}
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.Join(lines, ""))
	if err != nil || len(decoded) > maximum {
		return nil, errors.New("invalid_base64")
	}
	return decoded, nil
}

var stateV3RefCreateVisibilityDelays = []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second}

func stateV3RefCreateVisibilitySchedule(created bool) ([]time.Duration, bool) {
	if !created {
		return nil, false
	}
	return append([]time.Duration(nil), stateV3RefCreateVisibilityDelays...), true
}
func stateV3ExactRefReread(ref StateV3WireRef, expectedRef, expectedOID, expectedType string) bool {
	return ref.Ref == expectedRef && ref.Object.SHA == expectedOID && ref.Object.Type == expectedType
}
func stateV3ExactCommitReread(commit StateV3WireCommit, oid, parent, tree, message string) bool {
	return commit.SHA == oid && commit.Tree.SHA == tree && commit.Message == message && len(commit.Parents) == 1 && commit.Parents[0].SHA == parent
}
func stateV3ExactTreeReread(tree StateV3WireTree, oid string, entries []StateV3StateTreeEntry) bool {
	observed, ok := tree.regularFileEntries()
	return ok && tree.SHA == oid && !tree.Truncated && len(observed) == len(entries) && sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path }) && func() bool {
		for i := range entries {
			if observed[i] != entries[i] {
				return false
			}
		}
		return true
	}()
}
func stateV3ExactBlobReread(blob StateV3WireBlob, oid string, expected []byte) bool {
	return blob.SHA == oid && bytes.Equal(blob.Raw, expected) && stateV3GitBlobOID(blob.Raw) == oid
}
func stateV3WireBodySHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func stateV3ValidatedResponseDigest(body []byte, digest string) bool {
	return len(body) > 0 && len(body) <= MaxGitHubResponseBytes && digest == stateV3WireBodySHA256(body)
}
