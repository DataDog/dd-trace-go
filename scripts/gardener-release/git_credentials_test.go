// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestProtectedGitCredentialsUseFixedNonSecretAskpass(t *testing.T) {
	const token = "test-token-that-must-not-be-written"
	t.Setenv(PublicationTokenEnvironment, token)
	credentials, err := LoadProtectedGitHubGitCredentials()
	if err != nil {
		t.Fatal(err)
	}
	directory := credentials.directory
	helper, err := os.ReadFile(credentials.askpass)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(helper), token) || !strings.Contains(string(helper), PublicationTokenEnvironment) {
		t.Fatalf("askpass embeds token or lacks fixed environment lookup: %q", helper)
	}
	command, err := credentials.Command(t.TempDir(), "ls-remote", CanonicalGitHubRemote, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	joinedArgs := strings.Join(command.Args, " ")
	if strings.Contains(joinedArgs, token) || !strings.Contains(joinedArgs, "credential.helper=") || !strings.Contains(joinedArgs, "http.followRedirects=false") {
		t.Fatalf("unsafe args: %v", command.Args)
	}
	joinedEnv := strings.Join(command.Env, "\n")
	for _, required := range []string{"GIT_ASKPASS_REQUIRE=force", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1"} {
		if !strings.Contains(joinedEnv, required) {
			t.Fatalf("env missing %s", required)
		}
	}
	result, err := (ExecRunner{}).Run(context.Background(), Command{Path: credentials.askpass, Args: []string{"Unknown prompt"}, Env: []string{PublicationTokenEnvironment + "=" + token}})
	if err == nil || strings.Contains(result.Stdout+result.Stderr, token) {
		t.Fatalf("unknown prompt result=%#v err=%v", result, err)
	}
	if err := credentials.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("auth directory remains: %v", err)
	}
}

func TestProtectedGitCredentialsRejectAllOtherRemotes(t *testing.T) {
	t.Setenv(PublicationTokenEnvironment, "test-token")
	credentials, err := LoadProtectedGitHubGitCredentials()
	if err != nil {
		t.Fatal(err)
	}
	defer credentials.Close()
	for _, args := range [][]string{{"push", "https://evil.example/DataDog/dd-trace-go.git", "x:y"}, {"push", "origin", "x:y"}, {"fetch", "http://github.com/DataDog/dd-trace-go.git"}} {
		if _, err := credentials.Command(t.TempDir(), args...); err == nil {
			t.Fatalf("args %v accepted", args)
		}
	}
}

func TestProtectedGitCredentialsMissingTokenFailsBeforeMaterialization(t *testing.T) {
	t.Setenv(PublicationTokenEnvironment, "")
	if _, err := LoadProtectedGitHubGitCredentials(); ErrorCode(err) != "publication_token_unavailable" {
		t.Fatalf("error = %q", ErrorCode(err))
	}
}
