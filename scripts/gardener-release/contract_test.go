// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type dispatchContract struct {
	WorkflowDispatch struct {
		RequiredStringInputs []string `json:"required_string_inputs"`
	} `json:"workflow_dispatch"`
}

type commandFixture struct {
	ID      string `json:"id"`
	Case    string `json:"case"`
	Body    string `json:"body"`
	Webhook struct {
		RepositoryID       string `json:"repository_id"`
		RepositoryFullName string `json:"repository_full_name"`
		IssueNumber        string `json:"issue_number"`
		OriginalCommentID  string `json:"original_comment_id"`
	} `json:"webhook"`
	Inputs   map[string]string `json:"inputs"`
	Expected struct {
		OK                bool   `json:"ok"`
		Name              string `json:"name"`
		Command           string `json:"command"`
		NormalizedVersion string `json:"normalized_version"`
		VersionExplicit   bool   `json:"version_explicit"`
		BodySnapshot      string `json:"body_snapshot"`
		RequestKey        string `json:"request_key"`
		Marker            string `json:"marker"`
		RequestSHA256     string `json:"request_sha256"`
		ErrorCode         string `json:"error_code"`
		Matrix            string `json:"matrix"`
	} `json:"expected"`
}

func fixturePaths(t *testing.T, kind string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "_docs", "release-contract", "v1", "fixtures", kind, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no %s fixtures found", kind)
	}
	sort.Strings(matches)
	return matches
}

func readFixture(t *testing.T, path string) commandFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture commandFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return fixture
}

func TestParseCommandValidFixtures(t *testing.T) {
	for _, path := range fixturePaths(t, "valid") {
		fixture := readFixture(t, path)
		if fixture.Case != "valid_command" {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			parsed, err := ParseCommandBody(fixture.Body, RequestIDs{
				RepositoryID:       fixture.Webhook.RepositoryID,
				RepositoryFullName: fixture.Webhook.RepositoryFullName,
				IssueNumber:        fixture.Webhook.IssueNumber,
				OriginalCommentID:  fixture.Webhook.OriginalCommentID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Name != fixture.Expected.Name || parsed.Command != fixture.Expected.Command || parsed.NormalizedVersion != fixture.Expected.NormalizedVersion {
				t.Fatalf("unexpected parse: %#v", parsed)
			}
			if parsed.VersionExplicit != fixture.Expected.VersionExplicit {
				t.Fatalf("VersionExplicit = %v, want %v", parsed.VersionExplicit, fixture.Expected.VersionExplicit)
			}
			if parsed.BodySnapshot != fixture.Expected.BodySnapshot || parsed.RequestKey != fixture.Expected.RequestKey || parsed.Marker != fixture.Expected.Marker {
				t.Fatalf("unexpected normalized request fields: %#v", parsed)
			}
		})
	}
}

func TestParseCommandInvalidFixtures(t *testing.T) {
	for _, path := range fixturePaths(t, "invalid") {
		fixture := readFixture(t, path)
		if fixture.Case != "invalid_command" {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			_, err := ParseCommandBody(fixture.Body, RequestIDs{
				RepositoryID:       fixture.Webhook.RepositoryID,
				RepositoryFullName: fixture.Webhook.RepositoryFullName,
				IssueNumber:        fixture.Webhook.IssueNumber,
				OriginalCommentID:  fixture.Webhook.OriginalCommentID,
			})
			if ErrorCode(err) != fixture.Expected.ErrorCode {
				t.Fatalf("error = %q, want %q", ErrorCode(err), fixture.Expected.ErrorCode)
			}
		})
	}
}

func TestDecodeDispatchValidFixtures(t *testing.T) {
	for _, path := range fixturePaths(t, "valid") {
		fixture := readFixture(t, path)
		if fixture.Case != "valid_dispatch" {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			decoded, err := DecodeDispatchInputs(fixture.Inputs)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.RequestKey != fixture.Expected.RequestKey || decoded.Marker != fixture.Expected.Marker || decoded.RequestSHA256 != fixture.Expected.RequestSHA256 {
				t.Fatalf("unexpected decoded request: %#v", decoded)
			}
		})
	}
}

func TestDecodeDispatchInvalidFixtures(t *testing.T) {
	for _, path := range fixturePaths(t, "invalid") {
		fixture := readFixture(t, path)
		if fixture.Case != "invalid_dispatch" {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			_, err := DecodeDispatchInputs(fixture.Inputs)
			if ErrorCode(err) != fixture.Expected.ErrorCode {
				t.Fatalf("error = %q, want %q", ErrorCode(err), fixture.Expected.ErrorCode)
			}
		})
	}
}

func TestDecodeContextRejectsInvalidUTF8(t *testing.T) {
	_, err := DecodeDispatchInputs(map[string]string{
		"contract_version": "1",
		"command":          "release:promote",
		"version":          "v2.11.0",
		"context":          string([]byte{0xff, 0xfe}),
	})
	if ErrorCode(err) != ErrInvalidContextJSON.Error() {
		t.Fatalf("error = %q, want %q", ErrorCode(err), ErrInvalidContextJSON)
	}
}

func TestWorkflowSkeletonDeclaresOnlyContractInputs(t *testing.T) {
	contractData, err := os.ReadFile(filepath.Join("..", "..", "_docs", "dd-trace-go-release-contract.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract dispatchContract
	if err := json.Unmarshal(contractData, &contract); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "gardener-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"permissions: {}", "contract_version:", "command:", "version:", "context:", "type: string"} {
		if !strings.Contains(text, want) {
			t.Fatalf("workflow skeleton missing %q", want)
		}
	}
	for _, forbidden := range []string{"actions/checkout", "dd-octo-sts", "secrets.", "github-script", "git push", "gh api"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("workflow skeleton contains side-effecting token %q", forbidden)
		}
	}
	inputs := []string{}
	lines := strings.Split(text, "\n")
	inInputs := false
	for _, line := range lines {
		if line == "    inputs:" {
			inInputs = true
			continue
		}
		if inInputs {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !strings.HasPrefix(line, "      ") {
				break
			}
			if strings.HasPrefix(line, "      ") && !strings.HasPrefix(line, "        ") && strings.HasSuffix(strings.TrimSpace(line), ":") && !strings.Contains(strings.TrimSpace(line), " ") {
				inputs = append(inputs, strings.TrimSuffix(strings.TrimSpace(line), ":"))
			}
		}
	}
	sort.Strings(inputs)
	want := append([]string(nil), contract.WorkflowDispatch.RequiredStringInputs...)
	sort.Strings(want)
	if strings.Join(inputs, ",") != strings.Join(want, ",") {
		t.Fatalf("inputs = %v, want %v", inputs, want)
	}
}
