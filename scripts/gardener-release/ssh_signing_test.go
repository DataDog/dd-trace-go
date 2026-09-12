// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ephemeralSSHPolicy(t *testing.T) (SSHSigningPolicy, string) {
	t.Helper()
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "key")
	runCommand(t, Command{Path: "ssh-keygen", Args: []string{"-q", "-t", "ed25519", "-N", "", "-C", "", "-f", privatePath}, Env: gitStateEnv(), Dir: dir})
	private, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(privatePath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(public))
	publicKey := fields[0] + " " + fields[1]
	wire, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wire)
	return SSHSigningPolicy{Principal: "gardener-release", PublicKey: publicKey, Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])}, string(private)
}

func TestSSHContentSignerRoundTripAndTamper(t *testing.T) {
	policy, secret := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, secret)
	key, err := LoadProtectedSSHSigningKey(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	signer, err := NewSSHContentSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	signature, publicKey, err := signer.Sign([]byte("state record"))
	if err != nil {
		t.Fatal(err)
	}
	if !signer.Verify([]byte("state record"), signature, publicKey) || signer.Verify([]byte("changed"), signature, publicKey) {
		t.Fatal("SSH content signature verification mismatch")
	}
}

func TestProtectedSSHSigningKeyMaterializesOutsideWorkspaceAndCleansUp(t *testing.T) {
	policy, private := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, private)
	key, err := LoadProtectedSSHSigningKey(policy)
	if err != nil {
		t.Fatal(err)
	}
	directory := key.directory
	if strings.HasPrefix(directory, mustGetwd(t)+string(os.PathSeparator)) {
		t.Fatalf("key directory %s is under workspace", directory)
	}
	for _, path := range []string{key.privateKeyPath, key.allowedSignersPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode/error = %v/%v", path, info.Mode().Perm(), err)
		}
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("temporary key directory remains: %v", err)
	}
}

func TestProtectedSSHSigningKeyRejectsPrivatePublicMismatchAndCleansUp(t *testing.T) {
	policy, _ := ephemeralSSHPolicy(t)
	_, differentPrivate := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, differentPrivate)
	if _, err := LoadProtectedSSHSigningKey(policy); ErrorCode(err) != "ssh_private_public_key_mismatch" {
		t.Fatalf("error = %q", ErrorCode(err))
	}
}

func TestProtectedSSHSigningPolicyRejectsMissingPlaceholderAndMismatch(t *testing.T) {
	policy, _ := ephemeralSSHPolicy(t)
	cases := []SSHSigningPolicy{{}, {Principal: "replace-me", PublicKey: policy.PublicKey, Fingerprint: policy.Fingerprint}, {Principal: policy.Principal, PublicKey: policy.PublicKey, Fingerprint: "SHA256:wrong"}}
	for _, candidate := range cases {
		if err := ValidateSSHSigningPolicy(candidate); err == nil {
			t.Fatalf("policy %#v accepted", candidate)
		}
	}
	policy, _ = ephemeralSSHPolicy(t)
	if err := ValidateSSHSigningPolicy(policy); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSignedCommitTreeCommandCreatesAndVerifiesSSHSignature(t *testing.T) {
	policy, private := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, private)
	key, err := LoadProtectedSSHSigningKey(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet", "-b", "main")
	blob := strings.TrimSpace(runGitStdin(t, dir, "signed\n", "hash-object", "-w", "--stdin"))
	tree := strings.TrimSpace(runGitStdin(t, dir, "100644 blob "+blob+"\tREADME.md\n", "mktree"))
	command, err := BuildSignedCommitTreeCommand("git", gitStateEnv(), dir, tree, "", "signed state", "gardener-release", "gardener-release@datadoghq.invalid", 1700000000, key)
	if err != nil {
		t.Fatal(err)
	}
	result := runCommand(t, command)
	sha := strings.TrimSpace(result.Stdout)
	if err := VerifySSHGitSignature(context.Background(), ExecRunner{}, dir, sha, false, key); err != nil {
		t.Fatal(err)
	}
	object := runGit(t, dir, "cat-file", "-p", sha)
	if !strings.Contains(object, "gpgsig -----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("commit lacks SSH signature: %s", object)
	}
}

func TestSignCommitWithSSHSignsCommitAndEveryTag(t *testing.T) {
	policy, private := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, private)
	key, err := LoadProtectedSSHSigningKey(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SignCommitWithSSH(context.Background(), ExecRunner{}, nil, ephemeralTestSigner(t), SignInput{Output: output, Manifest: manifest, Reader: reader.Read, WorkDir: reader.Dir, ToolDigest: strings.Repeat("a", 64), ValidatorDigest: strings.Repeat("b", 64), SigningIntent: &GitSigningIntent{Timestamp: 1700000000, Message: "release: " + output.ResolvedVersion, Principal: policy.Principal, Fingerprint: policy.Fingerprint}}, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySSHGitSignature(context.Background(), ExecRunner{}, reader.Dir, result.SignedOutput.ReleaseSHA, false, key); err != nil {
		t.Fatal(err)
	}
	for _, tag := range result.SignedOutput.Tags {
		if err := VerifySSHGitSignature(context.Background(), ExecRunner{}, reader.Dir, tag.TagObjectSHA, true, key); err != nil {
			t.Fatalf("tag %s: %v", tag.Name, err)
		}
	}
}

func runCommand(t *testing.T, command Command) CommandResult {
	t.Helper()
	result, err := (ExecRunner{}).Run(context.Background(), command)
	if err != nil {
		t.Fatalf("%s %v: %v\nstdout=%s\nstderr=%s", command.Path, command.Args, err, result.Stdout, result.Stderr)
	}
	return result
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
