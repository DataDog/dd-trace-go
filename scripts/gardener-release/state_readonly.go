// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

type sshPublicVerifier struct {
	directory      string
	allowedSigners string
	principal      string
	publicWire     []byte
}

func newSSHPublicVerifier(policy SSHSigningPolicy) (*sshPublicVerifier, error) {
	if err := ValidateSSHSigningPolicy(policy); err != nil {
		return nil, err
	}
	dir, err := osMkdirPrivateTemp("gardener-release-public-verifier-")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(policy.PublicKey)
	wire, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_ssh_signing_policy")
	}
	path := filepath.Join(dir, "allowed-signers")
	if err := os.WriteFile(path, []byte(policy.Principal+" "+policy.PublicKey+"\n"), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "ssh_allowed_signers_materialize_failed")
	}
	return &sshPublicVerifier{directory: dir, allowedSigners: path, principal: policy.Principal, publicWire: wire}, nil
}

func (v *sshPublicVerifier) Verify(data, signature, publicKey []byte) bool {
	if v == nil || !bytes.Equal(publicKey, v.publicWire) || len(data) == 0 || len(signature) == 0 {
		return false
	}
	path := filepath.Join(v.directory, "signature")
	if os.WriteFile(path, signature, 0600) != nil {
		return false
	}
	defer os.Remove(path)
	_, err := (ExecRunner{}).Run(context.Background(), Command{Path: "ssh-keygen", Args: []string{"-Y", "verify", "-f", v.allowedSigners, "-I", v.principal, "-n", "gardener-release-state", "-s", path}, Env: gitStateEnv(), Dir: v.directory, Stdin: string(data)})
	return err == nil
}

func (v *sshPublicVerifier) Close() error {
	if v == nil || v.directory == "" {
		return nil
	}
	dir := v.directory
	v.directory = ""
	return os.RemoveAll(dir)
}

func verifySSHCommitWithPublicPolicy(ctx context.Context, runner CommandRunner, dir, sha string, policy SSHSigningPolicy, allowedSigners string) error {
	result, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "-c", "gpg.format=ssh", "-c", "gpg.ssh.allowedSignersFile=" + allowedSigners, "verify-commit", "--raw", sha}, Env: gitStateEnv(), Dir: dir})
	if err != nil {
		return wrapReleaseError(ErrorClassStateConflict, "ssh_git_signature_invalid", err)
	}
	evidence := result.Stdout + "\n" + result.Stderr
	if !strings.Contains(evidence, policy.Principal) || !strings.Contains(evidence, policy.Fingerprint) {
		return newReleaseError(ErrorClassStateConflict, "ssh_git_signer_mismatch")
	}
	return nil
}

// LoadProductionStateReadOnly verifies an exact signed remote state snapshot
// without loading a private key or any write credential.
func LoadProductionStateReadOnly(ctx context.Context, record Record, expectedHead string, policy Policy) (loaded Record, err error) {
	if !ValidGitObjectID(expectedHead) || !validRecordRepositoryBinding(record.Reservation) {
		return Record{}, newReleaseError(ErrorClassStateConflict, "state_read_input_invalid")
	}
	verifier, err := newSSHPublicVerifier(policy.Signing)
	if err != nil {
		return Record{}, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, verifier.Close()) }()
	dir, err := osMkdirPrivateTemp("gardener-release-state-read-")
	if err != nil {
		return Record{}, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(dir)) }()
	store := NewGitStateStore(ExecRunner{}, CanonicalGitHubRemote, StateBranch, dir, nil, verifier, verifier.publicWire, CommitIdentity{}, RealClock{})
	if err := store.InitWorkingRepository(ctx); err != nil {
		return Record{}, err
	}
	state, err := store.LoadState(ctx, record.Reservation.RequestKey)
	if err != nil {
		return Record{}, err
	}
	if !state.Found || state.RemoteHead != expectedHead || !recordsEqual(state.Record, record) {
		return Record{}, newReleaseError(ErrorClassStateConflict, "state_changed_before_read_phase")
	}
	if err := verifySSHCommitWithPublicPolicy(ctx, ExecRunner{}, dir, state.RemoteHead, policy.Signing, verifier.allowedSigners); err != nil {
		return Record{}, err
	}
	return state.Record, nil
}
