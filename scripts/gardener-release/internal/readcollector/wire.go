// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package readcollector

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The collector owns a faithful, private copy of the v3 endpoint contracts.
// It cannot import the parent package's wire implementation without a cycle.
type wireRef struct {
	Ref    string `json:"ref"`
	NodeID string `json:"node_id"`
	URL    string `json:"url"`
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"object"`
}
type wireIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}
type wireTreeRef struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}
type wireParent struct {
	SHA     string `json:"sha"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}
type wireVerification struct {
	Verified   *bool   `json:"verified"`
	Reason     string  `json:"reason"`
	Signature  *string `json:"signature"`
	Payload    *string `json:"payload"`
	VerifiedAt *string `json:"verified_at"`
}
type wireRawCommit struct {
	SHA          string           `json:"sha"`
	NodeID       string           `json:"node_id"`
	URL          string           `json:"url"`
	HTMLURL      string           `json:"html_url"`
	Author       wireIdentity     `json:"author"`
	Committer    wireIdentity     `json:"committer"`
	Tree         wireTreeRef      `json:"tree"`
	Message      string           `json:"message"`
	Parents      []wireParent     `json:"parents"`
	Verification wireVerification `json:"verification"`
}
type wireRESTIdentity struct {
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
	SiteAdmin         *bool  `json:"site_admin"`
}
type wireRESTFile struct {
	SHA              string  `json:"sha"`
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	Additions        *int    `json:"additions"`
	Deletions        *int    `json:"deletions"`
	Changes          *int    `json:"changes"`
	BlobURL          string  `json:"blob_url"`
	RawURL           string  `json:"raw_url"`
	ContentsURL      string  `json:"contents_url"`
	Patch            *string `json:"patch,omitempty"`
	PreviousFilename *string `json:"previous_filename,omitempty"`
}

type wireRESTCommit struct {
	SHA         string `json:"sha"`
	NodeID      string `json:"node_id"`
	URL         string `json:"url"`
	HTMLURL     string `json:"html_url"`
	CommentsURL string `json:"comments_url"`
	Commit      struct {
		Author       wireIdentity     `json:"author"`
		Committer    wireIdentity     `json:"committer"`
		Message      string           `json:"message"`
		Tree         wireTreeRef      `json:"tree"`
		URL          string           `json:"url"`
		CommentCount *int             `json:"comment_count"`
		Verification wireVerification `json:"verification"`
	} `json:"commit"`
	Author    *wireRESTIdentity `json:"author"`
	Committer *wireRESTIdentity `json:"committer"`
	Parents   []wireParent      `json:"parents"`
	Stats     struct {
		Total     *int `json:"total"`
		Additions *int `json:"additions"`
		Deletions *int `json:"deletions"`
	} `json:"stats"`
	Files []wireRESTFile `json:"files"`
}
type wireTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size,omitempty"`
	URL  string `json:"url"`
}
type wireTree struct {
	SHA       string          `json:"sha"`
	URL       string          `json:"url"`
	Truncated *bool           `json:"truncated"`
	Tree      []wireTreeEntry `json:"tree"`
}
type wireBlob struct {
	SHA      string `json:"sha"`
	NodeID   string `json:"node_id"`
	Size     *int64 `json:"size"`
	URL      string `json:"url"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Raw      []byte `json:"-"`
}
type wireTag struct {
	NodeID  string       `json:"node_id"`
	SHA     string       `json:"sha"`
	URL     string       `json:"url"`
	Tag     string       `json:"tag"`
	Message string       `json:"message"`
	Tagger  wireIdentity `json:"tagger"`
	Object  struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
		URL  string `json:"url"`
	} `json:"object"`
	Verification wireVerification `json:"verification"`
}
type wireGQLIdentity struct {
	Typename   string `json:"__typename"`
	Login      string `json:"login"`
	DatabaseID *int64 `json:"databaseId"`
}
type wireGQLActor struct {
	Name  string           `json:"name"`
	Email string           `json:"email"`
	Date  string           `json:"date"`
	User  *wireGQLIdentity `json:"user"`
}
type wireGQLCommit struct {
	Data struct {
		Repository struct {
			Object struct {
				Typename  string       `json:"__typename"`
				OID       string       `json:"oid"`
				Author    wireGQLActor `json:"author"`
				Committer wireGQLActor `json:"committer"`
				Signature struct {
					IsValid           *bool            `json:"isValid"`
					State             string           `json:"state"`
					WasSignedByGitHub *bool            `json:"wasSignedByGitHub"`
					Signer            *wireGQLIdentity `json:"signer"`
				} `json:"signature"`
			} `json:"object"`
		} `json:"repository"`
	} `json:"data"`
}

func decodeWire(raw []byte, target any, nullable ...string) bool {
	if len(raw) == 0 || len(raw) > maxResponseBytes || scanWire(raw, nullable...) != nil {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return false
	}
	var tail any
	return errors.Is(d.Decode(&tail), io.EOF)
}
func scanWire(raw []byte, nullable ...string) error {
	allowed := map[string]bool{}
	for _, p := range nullable {
		allowed[p] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanValue(d, nil, allowed); err != nil {
		return err
	}
	_, err := d.Token()
	if !errors.Is(err, io.EOF) {
		return errors.New("trailing")
	}
	return nil
}
func scanValue(d *json.Decoder, path []string, allowed map[string]bool) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		if !allowed[strings.Join(path, ".")] {
			return errors.New("null")
		}
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return errors.New("duplicate")
			}
			seen[s] = true
			if e = scanValue(d, append(path, s), allowed); e != nil {
				return e
			}
		}
		e := mustEnd(d, '}')
		if e != nil {
			return e
		}
	case '[':
		for d.More() {
			if e := scanValue(d, append(path, "[]"), allowed); e != nil {
				return e
			}
		}
		if e := mustEnd(d, ']'); e != nil {
			return e
		}
	}
	return nil
}
func mustEnd(d *json.Decoder, want json.Delim) error {
	t, e := d.Token()
	if e != nil || t != want {
		return errors.New("end")
	}
	return nil
}
func hasFields(raw []byte, path []string, fields ...string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	for _, p := range path {
		v, ok := m[p]
		if !ok || json.Unmarshal(v, &m) != nil {
			return false
		}
	}
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			return false
		}
	}
	return true
}

// validText, validIdentity, validRESTID, and validGQLID faithfully mirror
// the accepted v3 schema grammar. This package cannot import its parent
// without a cycle, so local copies keep transport evidence from being weaker
// than the schema evidence that will later consume it.
func validText(v string, max int) bool {
	return v != "" && len(v) <= max && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\r\n\x00") && !looksPlaceholder(v)
}

func looksPlaceholder(v string) bool {
	lower := strings.ToLower(v)
	return strings.Contains(lower, "placeholder") || strings.Contains(lower, "replace-me") || strings.Contains(lower, "todo") || strings.Contains(lower, "changeme")
}

func validToken(v string) bool {
	return validText(v, 128) && !strings.ContainsAny(v, " /\\:@")
}

func validGitIdentityPart(v string, max int) bool {
	if !validText(v, max) || strings.ContainsAny(v, "<>") {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validEmail(v string) bool {
	if !validGitIdentityPart(v, 254) || strings.Count(v, "@") != 1 || strings.Contains(v, " ") {
		return false
	}
	parts := strings.Split(v, "@")
	if parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[0], ".") || strings.HasSuffix(parts[0], ".") || strings.Contains(parts[0], "..") || !strings.Contains(parts[1], ".") {
		return false
	}
	for _, r := range parts[0] {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(".!#$%&'*+/=?^_`{|}~-[]", r)) {
			return false
		}
	}
	for _, label := range strings.Split(parts[1], ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-') {
				return false
			}
		}
	}
	return true
}

func validRepositoryRelativePath(v string) bool {
	if !validText(v, 4096) || strings.HasPrefix(v, "/") || strings.ContainsAny(v, "\\ \t<>") {
		return false
	}
	for _, component := range strings.Split(v, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func validDate(v string) bool {
	t, e := time.Parse(time.RFC3339, v)
	return e == nil && t.Format(time.RFC3339) == v
}
func validIdentity(v wireIdentity) bool {
	return validGitIdentityPart(v.Name, 128) && validEmail(v.Email) && validDate(v.Date)
}
func validVerification(v wireVerification) bool {
	return v.Verified != nil && *v.Verified && validText(v.Reason, 128) && v.Signature != nil && *v.Signature != "" && v.Payload != nil && *v.Payload != "" && v.VerifiedAt != nil && validDate(*v.VerifiedAt)
}
func validUnsigned(v wireVerification) bool {
	return v.Verified != nil && !(*v.Verified) && v.Reason == "unsigned" && v.Signature == nil && v.Payload == nil && v.VerifiedAt == nil
}
func validRESTID(v *wireRESTIdentity, optional bool) bool {
	if v == nil {
		return optional
	}
	return validToken(v.Login) && v.ID > 0 && validText(v.NodeID, 1024) && v.SiteAdmin != nil && validText(v.AvatarURL, 4096) && validText(v.HTMLURL, 4096) && validText(v.URL, 4096) && validText(v.FollowersURL, 4096) && validText(v.FollowingURL, 4096) && validText(v.GistsURL, 4096) && validText(v.StarredURL, 4096) && validText(v.SubscriptionsURL, 4096) && validText(v.OrganizationsURL, 4096) && validText(v.ReposURL, 4096) && validText(v.EventsURL, 4096) && validText(v.ReceivedEventsURL, 4096) && (v.Type == "User" || v.Type == "Bot") && validText(v.UserViewType, 128)
}
func validGQLID(v *wireGQLIdentity, optional bool) bool {
	if v == nil {
		return optional
	}
	return (v.Typename == "User" || v.Typename == "Bot") && validToken(v.Login) && v.DatabaseID != nil && *v.DatabaseID > 0
}
func decodeRef(raw []byte, ref, typ string) (wireRef, bool) {
	var v wireRef
	ok := decodeWire(raw, &v) && hasFields(raw, nil, "ref", "node_id", "url", "object") && v.Ref == ref && v.Object.Type == typ && validOID(v.Object.SHA) && validText(v.NodeID, 4096) && validText(v.URL, 4096) && validText(v.Object.URL, 4096)
	return v, ok
}
func decodeRawCommit(raw []byte, oid string) (wireRawCommit, bool) {
	var v wireRawCommit
	ok := decodeWire(raw, &v) && hasFields(raw, nil, "sha", "node_id", "url", "html_url", "author", "committer", "tree", "message", "parents", "verification") && v.SHA == oid && validOID(v.Tree.SHA) && validIdentity(v.Author) && validIdentity(v.Committer) && validText(v.Message, 8192) && validText(v.NodeID, 4096) && validText(v.URL, 4096) && validText(v.HTMLURL, 4096) && validText(v.Tree.URL, 4096) && len(v.Parents) <= 1 && validVerification(v.Verification)
	for _, p := range v.Parents {
		ok = ok && validOID(p.SHA) && validText(p.URL, 4096) && validText(p.HTMLURL, 4096)
	}
	return v, ok
}
func decodeRESTCommit(raw []byte, oid string) (wireRESTCommit, bool) {
	var v wireRESTCommit
	ok := decodeWire(raw, &v, "author", "committer") && hasFields(raw, nil, "sha", "node_id", "url", "html_url", "comments_url", "commit", "author", "committer", "parents", "stats", "files") && hasFields(raw, []string{"commit"}, "author", "committer", "message", "tree", "url", "comment_count", "verification") && v.SHA == oid && validText(v.NodeID, 4096) && validText(v.URL, 4096) && validText(v.HTMLURL, 4096) && validText(v.CommentsURL, 4096) && validIdentity(v.Commit.Author) && validIdentity(v.Commit.Committer) && validText(v.Commit.Message, 8192) && validOID(v.Commit.Tree.SHA) && validText(v.Commit.Tree.URL, 4096) && validText(v.Commit.URL, 4096) && v.Commit.CommentCount != nil && *v.Commit.CommentCount >= 0 && validVerification(v.Commit.Verification) && validRESTID(v.Author, true) && validRESTID(v.Committer, true) && v.Stats.Total != nil && *v.Stats.Total >= 0 && v.Stats.Additions != nil && *v.Stats.Additions >= 0 && v.Stats.Deletions != nil && *v.Stats.Deletions >= 0 && validRESTCommitDetails(raw, v)
	return v, ok
}

func validRESTCommitDetails(raw []byte, value wireRESTCommit) bool {
	identityFields := []string{"login", "id", "node_id", "avatar_url", "gravatar_id", "html_url", "url", "followers_url", "following_url", "gists_url", "starred_url", "subscriptions_url", "organizations_url", "repos_url", "events_url", "received_events_url", "type", "user_view_type", "site_admin"}
	if value.Author != nil && !hasFields(raw, []string{"author"}, identityFields...) || value.Committer != nil && !hasFields(raw, []string{"committer"}, identityFields...) {
		return false
	}
	var document struct {
		Files   []map[string]json.RawMessage `json:"files"`
		Parents []map[string]json.RawMessage `json:"parents"`
	}
	if json.Unmarshal(raw, &document) != nil || len(document.Files) != len(value.Files) || len(document.Parents) != len(value.Parents) {
		return false
	}
	for _, parent := range value.Parents {
		if !validOID(parent.SHA) || !validText(parent.URL, 4096) || !validText(parent.HTMLURL, 4096) {
			return false
		}
	}
	for index, file := range document.Files {
		for _, field := range []string{"sha", "filename", "status", "additions", "deletions", "changes", "blob_url", "raw_url", "contents_url"} {
			if _, ok := file[field]; !ok {
				return false
			}
		}
		value := value.Files[index]
		if !validOID(value.SHA) || !validRepositoryRelativePath(value.Filename) || !validText(value.Status, 128) || value.Additions == nil || *value.Additions < 0 || value.Deletions == nil || *value.Deletions < 0 || value.Changes == nil || *value.Changes < 0 || !validText(value.BlobURL, 4096) || !validText(value.RawURL, 4096) || !validText(value.ContentsURL, 4096) {
			return false
		}
	}
	return true
}
func decodeGQLCommit(raw []byte, oid string) (wireGQLCommit, bool) {
	var v wireGQLCommit
	ok := decodeWire(raw, &v, "data.repository.object.author.user", "data.repository.object.committer.user") && hasFields(raw, []string{"data", "repository", "object"}, "__typename", "oid", "author", "committer", "signature") && hasFields(raw, []string{"data", "repository", "object", "author"}, "name", "email", "date", "user") && hasFields(raw, []string{"data", "repository", "object", "committer"}, "name", "email", "date", "user") && hasFields(raw, []string{"data", "repository", "object", "signature"}, "isValid", "state", "wasSignedByGitHub", "signer") && v.Data.Repository.Object.Typename == "Commit" && v.Data.Repository.Object.OID == oid && validIdentity(wireIdentity{v.Data.Repository.Object.Author.Name, v.Data.Repository.Object.Author.Email, v.Data.Repository.Object.Author.Date}) && validIdentity(wireIdentity{v.Data.Repository.Object.Committer.Name, v.Data.Repository.Object.Committer.Email, v.Data.Repository.Object.Committer.Date}) && validGQLID(v.Data.Repository.Object.Author.User, false) && validGQLID(v.Data.Repository.Object.Committer.User, true) && v.Data.Repository.Object.Signature.IsValid != nil && *v.Data.Repository.Object.Signature.IsValid && v.Data.Repository.Object.Signature.State == "VALID" && v.Data.Repository.Object.Signature.WasSignedByGitHub != nil && *v.Data.Repository.Object.Signature.WasSignedByGitHub && validGQLID(v.Data.Repository.Object.Signature.Signer, false)
	return v, ok
}
func decodeTree(raw []byte, oid string) (wireTree, bool) {
	var v wireTree
	ok := decodeWire(raw, &v) && hasFields(raw, nil, "sha", "url", "truncated", "tree") && v.SHA == oid && validText(v.URL, 4096) && v.Truncated != nil && !(*v.Truncated) && len(v.Tree) > 0 && len(v.Tree) <= 10000
	seen := map[string]bool{}
	for _, e := range v.Tree {
		good := validRepositoryRelativePath(e.Path) && !seen[e.Path] && validOID(e.SHA) && validText(e.URL, 4096)
		switch {
		case e.Type == "tree" && e.Mode == "040000" && e.Size == nil:
		case e.Type == "blob" && (e.Mode == "100644" || e.Mode == "100755" || e.Mode == "120000") && e.Size != nil && *e.Size >= 0:
		case e.Type == "commit" && e.Mode == "160000" && e.Size == nil:
		default:
			good = false
		}
		ok = ok && good
		seen[e.Path] = true
	}
	return v, ok
}
func decodeBlob(raw []byte, oid string, max int) (wireBlob, bool) {
	var v wireBlob
	ok := decodeWire(raw, &v) && hasFields(raw, nil, "sha", "node_id", "size", "url", "content", "encoding") && v.SHA == oid && validText(v.NodeID, 4096) && v.Size != nil && *v.Size > 0 && validText(v.URL, 4096) && v.Encoding == "base64"
	if !ok {
		return v, false
	}
	d, e := decodeB64(v.Content, max)
	if e != nil || int64(len(d)) != *v.Size || gitBlobOID(d) != oid {
		return v, false
	}
	v.Raw = d
	return v, true
}
func decodeTag(raw []byte, oid, name, commit string) (wireTag, bool) {
	var v wireTag
	ok := decodeWire(raw, &v, "verification.signature", "verification.payload", "verification.verified_at") && hasFields(raw, nil, "node_id", "sha", "url", "tag", "message", "tagger", "object", "verification") && hasFields(raw, []string{"verification"}, "verified", "reason", "signature", "payload", "verified_at") && v.SHA == oid && v.Tag == name && v.Message == name+"\n" && v.Object.Type == "commit" && v.Object.SHA == commit && validText(v.NodeID, 4096) && validText(v.URL, 4096) && validText(v.Object.URL, 4096) && validIdentity(v.Tagger) && validUnsigned(v.Verification)
	return v, ok
}
func decodeB64(s string, max int) ([]byte, error) {
	if s == "" || strings.ContainsAny(s, "\r\t ") {
		return nil, errors.New("base64")
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		if l == "" || (i < len(lines)-1 && len(l) != 76) || len(l) > 76 {
			return nil, errors.New("base64")
		}
	}
	b, e := base64.StdEncoding.Strict().DecodeString(strings.Join(lines, ""))
	if e != nil || len(b) > max {
		return nil, errors.New("base64")
	}
	return b, nil
}
func gitBlobOID(raw []byte) string {
	h := sha1.New()
	_, _ = h.Write([]byte("blob " + strconv.Itoa(len(raw)) + "\x00"))
	_, _ = h.Write(raw)
	return fmtHex(h.Sum(nil))
}
func fmtHex(b []byte) string {
	const x = "0123456789abcdef"
	var out strings.Builder
	out.Grow(len(b) * 2)
	for _, v := range b {
		out.WriteByte(x[v>>4])
		out.WriteByte(x[v&15])
	}
	return out.String()
}
func regularEntries(v wireTree) ([]wireTreeEntry, bool) {
	out := make([]wireTreeEntry, 0, len(v.Tree))
	for _, e := range v.Tree {
		if e.Type == "tree" {
			continue
		}
		if e.Type != "blob" || e.Mode != "100644" || e.Size == nil {
			return nil, false
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, true
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneVerification(value wireVerification) wireVerification {
	return wireVerification{
		Verified: cloneBool(value.Verified), Reason: value.Reason, Signature: cloneString(value.Signature), Payload: cloneString(value.Payload), VerifiedAt: cloneString(value.VerifiedAt),
	}
}

func cloneRawCommit(value wireRawCommit) wireRawCommit {
	copy := value
	copy.Parents = append([]wireParent(nil), value.Parents...)
	copy.Verification = cloneVerification(value.Verification)
	return copy
}

func cloneRESTIdentity(value *wireRESTIdentity) *wireRESTIdentity {
	if value == nil {
		return nil
	}
	copy := *value
	copy.SiteAdmin = cloneBool(value.SiteAdmin)
	return &copy
}

func cloneRESTCommit(value wireRESTCommit) wireRESTCommit {
	copy := value
	copy.Commit.CommentCount = cloneInt(value.Commit.CommentCount)
	copy.Commit.Verification = cloneVerification(value.Commit.Verification)
	copy.Author = cloneRESTIdentity(value.Author)
	copy.Committer = cloneRESTIdentity(value.Committer)
	copy.Parents = append([]wireParent(nil), value.Parents...)
	copy.Stats.Total = cloneInt(value.Stats.Total)
	copy.Stats.Additions = cloneInt(value.Stats.Additions)
	copy.Stats.Deletions = cloneInt(value.Stats.Deletions)
	copy.Files = append([]wireRESTFile(nil), value.Files...)
	for index := range copy.Files {
		copy.Files[index].Additions = cloneInt(value.Files[index].Additions)
		copy.Files[index].Deletions = cloneInt(value.Files[index].Deletions)
		copy.Files[index].Changes = cloneInt(value.Files[index].Changes)
		copy.Files[index].Patch = cloneString(value.Files[index].Patch)
		copy.Files[index].PreviousFilename = cloneString(value.Files[index].PreviousFilename)
	}
	return copy
}

func cloneGQLIdentity(value *wireGQLIdentity) *wireGQLIdentity {
	if value == nil {
		return nil
	}
	copy := *value
	copy.DatabaseID = cloneInt64(value.DatabaseID)
	return &copy
}

func cloneGQLCommit(value wireGQLCommit) wireGQLCommit {
	copy := value
	copy.Data.Repository.Object.Author.User = cloneGQLIdentity(value.Data.Repository.Object.Author.User)
	copy.Data.Repository.Object.Committer.User = cloneGQLIdentity(value.Data.Repository.Object.Committer.User)
	copy.Data.Repository.Object.Signature.IsValid = cloneBool(value.Data.Repository.Object.Signature.IsValid)
	copy.Data.Repository.Object.Signature.WasSignedByGitHub = cloneBool(value.Data.Repository.Object.Signature.WasSignedByGitHub)
	copy.Data.Repository.Object.Signature.Signer = cloneGQLIdentity(value.Data.Repository.Object.Signature.Signer)
	return copy
}

func cloneTree(value wireTree) wireTree {
	copy := value
	copy.Truncated = cloneBool(value.Truncated)
	copy.Tree = append([]wireTreeEntry(nil), value.Tree...)
	for index := range copy.Tree {
		copy.Tree[index].Size = cloneInt64(value.Tree[index].Size)
	}
	return copy
}

func cloneBlob(value wireBlob) wireBlob {
	copy := value
	copy.Size = cloneInt64(value.Size)
	copy.Raw = append([]byte(nil), value.Raw...)
	return copy
}

func cloneTag(value wireTag) wireTag {
	copy := value
	copy.Verification = cloneVerification(value.Verification)
	return copy
}
