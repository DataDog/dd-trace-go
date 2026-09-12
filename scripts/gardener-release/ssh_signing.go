// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const ProtectedSSHSigningKeyEnvironment = "GARDENER_RELEASE_SSH_SIGNING_KEY"

// SSHSigningPolicy is reviewed, non-secret policy. Production has no default:
// all fields must be provisioned and attested before a protected job can sign.
type SSHSigningPolicy struct {
	Principal   string `json:"principal"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// ProtectedSSHSigningKey owns runner-temporary key material. Paths are private
// and cannot be selected by dispatch input or a CLI flag.
type ProtectedSSHSigningKey struct {
	directory          string
	privateKeyPath     string
	allowedSignersPath string
	policy             SSHSigningPolicy
}

// LoadProtectedSSHSigningKey reads exactly one fixed protected-environment
// secret and materializes it outside the workspace with restrictive modes.
func LoadProtectedSSHSigningKey(policy SSHSigningPolicy) (*ProtectedSSHSigningKey, error) {
	if err := ValidateSSHSigningPolicy(policy); err != nil {
		return nil, err
	}
	secret, ok := os.LookupEnv(ProtectedSSHSigningKeyEnvironment)
	if !ok || strings.TrimSpace(secret) == "" {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_unavailable")
	}
	directory, err := os.MkdirTemp("", "gardener-release-signing-")
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_materialize_failed", err)
	}
	key := &ProtectedSSHSigningKey{directory: directory, privateKeyPath: filepath.Join(directory, "signing-key"), allowedSignersPath: filepath.Join(directory, "allowed-signers"), policy: policy}
	cleanup := true
	defer func() {
		if cleanup {
			_ = key.Close()
		}
	}()
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_materialize_failed", err)
	}
	if err := os.WriteFile(key.privateKeyPath, []byte(secret), 0600); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_materialize_failed", err)
	}
	if err := os.WriteFile(key.allowedSignersPath, []byte(policy.Principal+" "+policy.PublicKey+"\n"), 0600); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_allowed_signers_materialize_failed", err)
	}
	derived, err := (ExecRunner{}).Run(context.Background(), Command{Path: "ssh-keygen", Args: []string{"-y", "-f", key.privateKeyPath}, Env: gitStateEnv(), Dir: directory})
	if err != nil || strings.TrimSpace(derived.Stdout) != policy.PublicKey {
		return nil, newReleaseError(ErrorClassContractMismatch, "ssh_private_public_key_mismatch")
	}
	cleanup = false
	return key, nil
}

func ValidateSSHSigningPolicy(policy SSHSigningPolicy) error {
	if policy.Principal == "" || strings.TrimSpace(policy.Principal) != policy.Principal || strings.ContainsAny(policy.Principal, " \t\r\n,\x00") || looksPlaceholder(policy.Principal) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_ssh_signing_policy")
	}
	fields := strings.Fields(policy.PublicKey)
	if len(fields) != 2 || (fields[0] != "ssh-ed25519" && !strings.HasPrefix(fields[0], "ecdsa-sha2-")) || strings.Join(fields, " ") != policy.PublicKey || looksPlaceholder(policy.PublicKey) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_ssh_signing_policy")
	}
	wire, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(wire) == 0 {
		return newReleaseError(ErrorClassContractMismatch, "invalid_ssh_signing_policy")
	}
	digest := sha256.Sum256(wire)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
	if policy.Fingerprint != want || looksPlaceholder(policy.Fingerprint) {
		return newReleaseError(ErrorClassContractMismatch, "ssh_signing_fingerprint_mismatch")
	}
	return nil
}

func looksPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "placeholder") || strings.Contains(lower, "replace-me") || strings.Contains(lower, "todo") || strings.Contains(lower, "changeme")
}

func (k *ProtectedSSHSigningKey) Close() error {
	if k == nil || k.directory == "" {
		return nil
	}
	directory := k.directory
	k.directory, k.privateKeyPath, k.allowedSignersPath = "", "", ""
	return os.RemoveAll(directory)
}

// SSHContentSigner uses the same protected SSH key for detached state
// envelopes. Git signatures remain the authoritative object signatures.
type SSHContentSigner struct {
	key *ProtectedSSHSigningKey
}

// NewSSHContentSigner returns a signer and verifier bound to reviewed policy.
func NewSSHContentSigner(key *ProtectedSSHSigningKey) (*SSHContentSigner, error) {
	if key == nil {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_unavailable")
	}
	if _, err := key.gitSigningArgs(); err != nil {
		return nil, err
	}
	return &SSHContentSigner{key: key}, nil
}

func (s *SSHContentSigner) publicWire() ([]byte, error) {
	if s == nil || s.key == nil {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_unavailable")
	}
	fields := strings.Fields(s.key.policy.PublicKey)
	if len(fields) != 2 {
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_ssh_signing_policy")
	}
	return base64.StdEncoding.DecodeString(fields[1])
}

func (s *SSHContentSigner) Sign(data []byte) ([]byte, []byte, error) {
	if len(data) == 0 || len(data) > MaxJobArtifactBytes {
		return nil, nil, newReleaseError(ErrorClassContractMismatch, "invalid_signing_payload")
	}
	file, err := os.CreateTemp(s.key.directory, "state-payload-")
	if err != nil {
		return nil, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_content_sign_failed", err)
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	defer os.Remove(path + ".sig")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_content_sign_failed", err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), Command{Path: "ssh-keygen", Args: []string{"-Y", "sign", "-f", s.key.privateKeyPath, "-n", "gardener-release-state", path}, Env: gitStateEnv(), Dir: s.key.directory}); err != nil {
		return nil, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "ssh_content_sign_failed", err)
	}
	signature, err := os.ReadFile(path + ".sig")
	if err != nil || len(signature) == 0 || len(signature) > 64*1024 {
		return nil, nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_content_sign_failed")
	}
	publicKey, err := s.publicWire()
	if err != nil {
		return nil, nil, err
	}
	return signature, publicKey, nil
}

func (s *SSHContentSigner) Verify(data, signature, publicKey []byte) bool {
	want, err := s.publicWire()
	if err != nil || !bytes.Equal(want, publicKey) || len(data) == 0 || len(data) > MaxJobArtifactBytes || len(signature) == 0 || len(signature) > 64*1024 {
		return false
	}
	file, err := os.CreateTemp(s.key.directory, "state-signature-")
	if err != nil {
		return false
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	if err := os.WriteFile(path, signature, 0600); err != nil {
		return false
	}
	_, err = (ExecRunner{}).Run(context.Background(), Command{Path: "ssh-keygen", Args: []string{"-Y", "verify", "-f", s.key.allowedSignersPath, "-I", s.key.policy.Principal, "-n", "gardener-release-state", "-s", path}, Env: gitStateEnv(), Dir: s.key.directory, Stdin: string(data)})
	return err == nil
}

func (k *ProtectedSSHSigningKey) gitSigningArgs() ([]string, error) {
	if k == nil || k.privateKeyPath == "" || k.allowedSignersPath == "" {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_unavailable")
	}
	for _, path := range []string{k.privateKeyPath, k.allowedSignersPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_signing_key_permissions_invalid")
		}
	}
	return []string{"-c", "gpg.format=ssh", "-c", "user.signingKey=" + k.privateKeyPath, "-c", "gpg.ssh.allowedSignersFile=" + k.allowedSignersPath}, nil
}

func BuildSignedCommitTreeCommand(gitPath string, env []string, dir, treeSHA, parentSHA, message, authorName, authorEmail string, authorDate int64, key *ProtectedSSHSigningKey) (Command, error) {
	base, err := BuildCommitTreeCommand(gitPath, env, dir, treeSHA, parentSHA, message, authorName, authorEmail, authorDate)
	if err != nil {
		return Command{}, err
	}
	signingArgs, err := key.gitSigningArgs()
	if err != nil {
		return Command{}, err
	}
	// Insert trusted config before the plumbing command and require -S.
	index := 4 // after protocol.file/core.hooksPath fixed config
	args := append([]string(nil), base.Args[:index]...)
	args = append(args, signingArgs...)
	args = append(args, base.Args[index:]...)
	for i, arg := range args {
		if arg == "commit-tree" {
			args = append(args[:i+1], append([]string{"-S"}, args[i+1:]...)...)
			break
		}
	}
	base.Args = args
	return base, nil
}

func VerifySSHGitSignature(ctx context.Context, runner CommandRunner, dir, objectSHA string, tag bool, key *ProtectedSSHSigningKey) error {
	if !ValidGitObjectID(objectSHA) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_git_object")
	}
	config, err := key.gitSigningArgs()
	if err != nil {
		return err
	}
	commandName := "verify-commit"
	if tag {
		commandName = "verify-tag"
	}
	args := append([]string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null"}, config...)
	args = append(args, commandName, "--raw", objectSHA)
	result, err := runner.Run(ctx, Command{Path: "git", Args: args, Env: gitStateEnv(), Dir: dir})
	if err != nil {
		return wrapReleaseError(ErrorClassStateConflict, "ssh_git_signature_invalid", err)
	}
	evidence := result.Stdout + "\n" + result.Stderr
	if !strings.Contains(evidence, key.policy.Principal) || !strings.Contains(evidence, key.policy.Fingerprint) {
		return newReleaseError(ErrorClassStateConflict, "ssh_git_signer_mismatch")
	}
	return nil
}

// ErrProtectedKeyCleanup is used only when a caller needs to join cleanup with
// a prior error without losing either signal.
func ErrProtectedKeyCleanup(prior, cleanup error) error {
	return errors.Join(prior, cleanup)
}
