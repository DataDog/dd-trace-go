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
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	ContractVersion     = "1"
	RepositoryFullName  = "DataDog/dd-trace-go"
	MaxBodyBytes        = 256
	MaxContextBytes     = 4096
	MaxVersionComponent = 2147483647
)

var (
	ErrCommandNotExact        = errors.New("command_not_exact")
	ErrControlCharacter       = errors.New("control_character")
	ErrDuplicateContextKey    = errors.New("duplicate_context_key")
	ErrExtraTokens            = errors.New("extra_tokens")
	ErrInvalidContractVersion = errors.New("invalid_contract_version")
	ErrInvalidContextJSON     = errors.New("invalid_context_json")
	ErrInvalidRepository      = errors.New("invalid_repository")
	ErrInvalidVersion         = errors.New("invalid_version")
	ErrMultipleCommands       = errors.New("multiple_commands")
	ErrNonASCII               = errors.New("non_ascii")
	ErrPreparePatchNotZero    = errors.New("prepare_patch_not_zero")
	ErrTrailingContextJSON    = errors.New("trailing_context_json")
	ErrUnknownContextKey      = errors.New("unknown_context_key")
	ErrUnknownInputKey        = errors.New("unknown_input_key")
	ErrUnknownReleaseCommand  = errors.New("unknown_release_command")
	ErrUnsafeID               = errors.New("unsafe_id")
	ErrVersionOverflow        = errors.New("version_overflow")
	ErrWrongContextType       = errors.New("wrong_context_type")
)

type RequestIDs struct {
	RepositoryID       string
	RepositoryFullName string
	IssueNumber        string
	OriginalCommentID  string
}

type ParsedCommand struct {
	Name               string
	Command            string
	NormalizedVersion  string
	VersionExplicit    bool
	BodySnapshot       string
	RepositoryID       string
	RepositoryFullName string
	IssueNumber        string
	OriginalCommentID  string
	RequestKey         string
	Marker             string
}

type Context struct {
	RepositoryID             string
	RepositoryFullName       string
	IssueNumber              string
	OriginalCommentID        string
	AcknowledgementCommentID string
	BodySnapshot             string
	PolicyRevision           string
}

type DispatchRequest struct {
	ContractVersion string
	Command         string
	Version         string
	Context         Context
	RequestKey      string
	Marker          string
	RequestSHA256   string
}

func ParseCommandBody(body string, ids RequestIDs) (ParsedCommand, error) {
	if !utf8.ValidString(body) {
		return ParsedCommand{}, ErrInvalidContextJSON
	}
	if len([]byte(body)) > MaxBodyBytes {
		return ParsedCommand{}, ErrCommandNotExact
	}
	if err := validateASCII(body); err != nil {
		return ParsedCommand{}, err
	}
	trimmed := strings.Trim(body, " \t")
	tokens := splitASCIISpace(trimmed)
	if countToken(tokens, "/gardener") > 1 {
		return ParsedCommand{}, ErrMultipleCommands
	}
	if len(tokens) < 2 || tokens[0] != "/gardener" || !strings.HasPrefix(tokens[1], "release") {
		return ParsedCommand{}, ErrCommandNotExact
	}
	if len(tokens) > 3 {
		return ParsedCommand{}, ErrExtraTokens
	}
	command := tokens[1]
	if !isReleaseCommand(command) {
		return ParsedCommand{}, ErrUnknownReleaseCommand
	}
	version := ""
	if len(tokens) == 3 {
		version = tokens[2]
	}
	normalized, explicit, err := NormalizeVersion(command, version)
	if err != nil {
		return ParsedCommand{}, err
	}
	if !validWebhookID(ids.RepositoryID) || !validWebhookID(ids.IssueNumber) || !validWebhookID(ids.OriginalCommentID) {
		return ParsedCommand{}, ErrUnsafeID
	}
	if ids.RepositoryFullName != RepositoryFullName {
		return ParsedCommand{}, ErrInvalidRepository
	}
	key := RequestKey(ids.RepositoryID, ids.OriginalCommentID)
	return ParsedCommand{
		Name:               "release_" + strings.TrimPrefix(command, "release:"),
		Command:            command,
		NormalizedVersion:  normalized,
		VersionExplicit:    explicit,
		BodySnapshot:       body,
		RepositoryID:       ids.RepositoryID,
		RepositoryFullName: ids.RepositoryFullName,
		IssueNumber:        ids.IssueNumber,
		OriginalCommentID:  ids.OriginalCommentID,
		RequestKey:         key,
		Marker:             Marker(ids.RepositoryID, ids.OriginalCommentID, command, normalized),
	}, nil
}

func NormalizeVersion(command, raw string) (string, bool, error) {
	if raw == "" {
		return "auto", false, nil
	}
	match := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\.(0|[1-9][0-9]*))?$`).FindStringSubmatch(raw)
	if match == nil {
		return "", false, ErrInvalidVersion
	}
	patch := "0"
	if match[3] != "" {
		patch = match[3]
	}
	parts := []string{match[1], match[2], patch}
	for _, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value > MaxVersionComponent {
			return "", false, ErrVersionOverflow
		}
	}
	if command == "release:prepare" && patch != "0" {
		return "", false, ErrPreparePatchNotZero
	}
	return fmt.Sprintf("v%s.%s.%s", match[1], match[2], patch), true, nil
}

func DecodeDispatchJSON(raw []byte) (DispatchRequest, error) {
	fields, err := decodeStrictObject(raw, MaxContextBytes+1024, ErrInvalidContextJSON, ErrTrailingContextJSON, ErrDuplicateContextKey)
	if err != nil {
		if errors.Is(err, errStrictJSONNotObject) {
			return DispatchRequest{}, ErrWrongContextType
		}
		return DispatchRequest{}, err
	}
	allowed := map[string]bool{"contract_version": true, "command": true, "version": true, "context": true}
	inputs := make(map[string]string, len(allowed))
	for key := range fields {
		if !allowed[key] {
			return DispatchRequest{}, ErrUnknownInputKey
		}
		s, err := stringField(fields, key, ErrUnknownInputKey, ErrWrongContextType)
		if err != nil {
			return DispatchRequest{}, err
		}
		inputs[key] = s
	}
	return DecodeDispatchInputs(inputs)
}

func DecodeDispatchInputs(inputs map[string]string) (DispatchRequest, error) {
	required := map[string]bool{"contract_version": true, "command": true, "version": true, "context": true}
	for key := range inputs {
		if !required[key] {
			return DispatchRequest{}, ErrUnknownInputKey
		}
	}
	for key := range required {
		if _, ok := inputs[key]; !ok {
			return DispatchRequest{}, ErrUnknownInputKey
		}
	}
	if inputs["contract_version"] != ContractVersion {
		return DispatchRequest{}, ErrInvalidContractVersion
	}
	if !isReleaseCommand(inputs["command"]) {
		return DispatchRequest{}, ErrUnknownReleaseCommand
	}
	if inputs["version"] != "auto" {
		normalized, _, err := NormalizeVersion(inputs["command"], inputs["version"])
		if err != nil {
			return DispatchRequest{}, err
		}
		if normalized != inputs["version"] {
			return DispatchRequest{}, ErrInvalidVersion
		}
	}
	if len([]byte(inputs["context"])) > MaxContextBytes || !utf8.ValidString(inputs["context"]) {
		return DispatchRequest{}, ErrInvalidContextJSON
	}
	ctx, err := DecodeContext(inputs["context"])
	if err != nil {
		return DispatchRequest{}, err
	}
	key := RequestKey(ctx.RepositoryID, ctx.OriginalCommentID)
	return DispatchRequest{
		ContractVersion: inputs["contract_version"],
		Command:         inputs["command"],
		Version:         inputs["version"],
		Context:         ctx,
		RequestKey:      key,
		Marker:          Marker(ctx.RepositoryID, ctx.OriginalCommentID, inputs["command"], inputs["version"]),
		RequestSHA256:   RequestSHA256(ctx, inputs["command"], inputs["version"]),
	}, nil
}

func DecodeContext(raw string) (Context, error) {
	seen, err := decodeStrictObject([]byte(raw), MaxContextBytes, ErrInvalidContextJSON, ErrTrailingContextJSON, ErrDuplicateContextKey)
	if err != nil {
		if errors.Is(err, errStrictJSONNotObject) {
			return Context{}, ErrWrongContextType
		}
		return Context{}, err
	}
	allowed := map[string]bool{
		"repository_id":              true,
		"repository_full_name":       true,
		"issue_number":               true,
		"original_comment_id":        true,
		"acknowledgement_comment_id": true,
		"body_snapshot":              true,
		"policy_revision":            true,
	}
	for key := range seen {
		if !allowed[key] {
			return Context{}, ErrUnknownContextKey
		}
	}
	value := func(key string) (string, error) {
		return stringField(seen, key, ErrUnknownContextKey, ErrWrongContextType)
	}
	ctx := Context{}
	if ctx.RepositoryID, err = value("repository_id"); err != nil {
		return Context{}, err
	}
	if ctx.RepositoryFullName, err = value("repository_full_name"); err != nil {
		return Context{}, err
	}
	if ctx.IssueNumber, err = value("issue_number"); err != nil {
		return Context{}, err
	}
	if ctx.OriginalCommentID, err = value("original_comment_id"); err != nil {
		return Context{}, err
	}
	if ctx.AcknowledgementCommentID, err = value("acknowledgement_comment_id"); err != nil {
		return Context{}, err
	}
	if ctx.BodySnapshot, err = value("body_snapshot"); err != nil {
		return Context{}, err
	}
	if ctx.PolicyRevision, err = value("policy_revision"); err != nil {
		return Context{}, err
	}
	if !validID(ctx.RepositoryID) || !validID(ctx.IssueNumber) || !validID(ctx.OriginalCommentID) || !validID(ctx.AcknowledgementCommentID) {
		return Context{}, ErrUnsafeID
	}
	if ctx.RepositoryFullName != RepositoryFullName {
		return Context{}, ErrInvalidRepository
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(ctx.PolicyRevision) {
		return Context{}, ErrWrongContextType
	}
	return ctx, nil
}

func RequestKey(repositoryID, originalCommentID string) string {
	return repositoryID + ":" + originalCommentID
}

func Marker(repositoryID, originalCommentID, command, version string) string {
	return fmt.Sprintf("<!-- gardener:release:request:v1:%s:%s command=%s version=%s -->", repositoryID, originalCommentID, command, version)
}

func RequestSHA256(ctx Context, command, version string) string {
	ordered := []string{ContractVersion, ctx.RepositoryID, ctx.RepositoryFullName, ctx.IssueNumber, ctx.OriginalCommentID, command, version, ctx.BodySnapshot}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func isReleaseCommand(command string) bool {
	switch command {
	case "release:prepare", "release:promote", "release:release":
		return true
	default:
		return false
	}
}

func splitASCIISpace(s string) []string {
	if s == "" {
		return nil
	}
	return regexp.MustCompile(`[ \t]+`).Split(s, -1)
}

func countToken(tokens []string, want string) int {
	count := 0
	for _, token := range tokens {
		if token == want {
			count++
		}
	}
	return count
}

func validateASCII(s string) error {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\n' || b == '\r' || b == 0 || (b < 32 && b != '\t') || b == 127 {
			return ErrControlCharacter
		}
		if b > 126 {
			return ErrNonASCII
		}
	}
	return nil
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 20 {
		return false
	}
	if s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validWebhookID(s string) bool {
	if !validID(s) {
		return false
	}
	value, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return false
	}
	return value <= 9007199254740991
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var releaseErr *ReleaseError
	if errors.As(err, &releaseErr) {
		return releaseErr.Code
	}
	return err.Error()
}

func EqualFixtureBytes(left, right []byte) bool {
	return bytes.Equal(left, right)
}
