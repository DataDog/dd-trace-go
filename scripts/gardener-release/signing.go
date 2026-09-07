// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Signer signs arbitrary bytes. B08 reuses this interface to sign release
// commits and recovery bundle manifests, so it must stay generic: it knows
// nothing about state records, Git objects, or release semantics.
type Signer interface {
	// Sign returns a signature over data and the verification key that a
	// Verifier can use to check it. The key is not secret.
	Sign(data []byte) (signature, publicKey []byte, err error)
}

// Verifier checks a signature produced by a Signer against a fixed,
// approved public key. Production code must not decide the trusted key
// from data being verified; the trusted key is caller-supplied.
type Verifier interface {
	Verify(data, signature, publicKey []byte) bool
}

// Ed25519Signer signs with a fixed keypair held only in memory. It never
// persists key material and never logs the private key.
type Ed25519Signer struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
}

// NewEphemeralSigner generates a fresh in-memory Ed25519 keypair. Tests use
// this to create disposable signing identities that never touch disk.
// Production code must load a fixed, reviewed key through a separate,
// explicitly audited path; this constructor is for ephemeral/test use.
func NewEphemeralSigner() (*Ed25519Signer, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "signing_key_generation_failed", err)
	}
	return &Ed25519Signer{private: private, public: public}, nil
}

// NewSignerFromSeed builds a deterministic signer from a fixed 32-byte
// seed. It exists for reproducible test fixtures; production signing key
// material must never be embedded as a literal seed in source or logs.
func NewSignerFromSeed(seed []byte) (*Ed25519Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_signing_seed")
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	return &Ed25519Signer{private: private, public: public}, nil
}

func (s *Ed25519Signer) Sign(data []byte) (signature, publicKey []byte, err error) {
	if s == nil {
		return nil, nil, newReleaseError(ErrorClassEvidenceIncomplete, "signer_unavailable")
	}
	return ed25519.Sign(s.private, data), append([]byte(nil), s.public...), nil
}

// PublicKey returns the verification key. It is not secret and is safe to
// persist alongside signed records.
func (s *Ed25519Signer) PublicKey() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.public...)
}

// Ed25519Verifier checks Ed25519 signatures. It holds no key material of
// its own; every call receives the caller's approved public key.
type Ed25519Verifier struct{}

func (Ed25519Verifier) Verify(data, signature, publicKey []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), data, signature)
}

// SignedEnvelope pairs signed bytes with their detached signature and the
// public key that verifies them. It is the on-disk shape used by state
// records; B08 reuses it for release commit and recovery bundle manifests.
type SignedEnvelope struct {
	Data      []byte `json:"data"`
	Signature string `json:"signature_hex"`
	PublicKey string `json:"public_key_hex"`
}

// Seal signs data and returns a SignedEnvelope ready to persist.
func Seal(signer Signer, data []byte) (SignedEnvelope, error) {
	signature, publicKey, err := signer.Sign(data)
	if err != nil {
		return SignedEnvelope{}, err
	}
	return SignedEnvelope{
		Data:      append([]byte(nil), data...),
		Signature: hex.EncodeToString(signature),
		PublicKey: hex.EncodeToString(publicKey),
	}, nil
}

// Open verifies a SignedEnvelope against trustedPublicKey and returns its
// data. trustedPublicKey must come from reviewed configuration, never from
// the envelope itself: an envelope's own embedded key cannot authenticate it.
func Open(verifier Verifier, envelope SignedEnvelope, trustedPublicKey []byte) ([]byte, error) {
	signature, err := hex.DecodeString(envelope.Signature)
	if err != nil {
		return nil, newReleaseError(ErrorClassStateConflict, "invalid_state_signature")
	}
	if !verifier.Verify(envelope.Data, signature, trustedPublicKey) {
		return nil, newReleaseError(ErrorClassStateConflict, "invalid_state_signature")
	}
	return envelope.Data, nil
}

// BuildCommitTreeCommand builds a fixed-argument `git commit-tree` command
// using Git's own plumbing rather than shelling through `commit`. It does
// not produce a GitHub-verifiable Git signature (`gpg.format=ssh`,
// `commit.gpgsign=true`): that requires a registered signing key and
// GitHub-side verification, which is a B13 administrator-attested
// production concern. Instead, state.go authenticates each stored record
// at the content layer with Seal/Open (Signer/Verifier below), which is
// sufficient for this step's local-fixture-only state store and is the
// same generic mechanism B08 reuses for release commits and recovery
// bundle manifests. Do not present the resulting commit as cryptographically
// signed at the Git level; it is only well-formed and content-authenticated.
//
// The wrapper never calls this against a real remote in this step: callers
// supply dir pointing at a local fixture repository, and the returned
// Command is executed through the existing CommandRunner seam.
func BuildCommitTreeCommand(gitPath string, env []string, dir, treeSHA, parentSHA, message, authorName, authorEmail string, authorDate int64) (Command, error) {
	if !ValidGitObjectID(treeSHA) {
		return Command{}, newReleaseError(ErrorClassContractMismatch, "invalid_git_object")
	}
	if parentSHA != "" && !ValidGitObjectID(parentSHA) {
		return Command{}, newReleaseError(ErrorClassContractMismatch, "invalid_git_object")
	}
	if authorName == "" || authorEmail == "" || message == "" {
		return Command{}, newReleaseError(ErrorClassContractMismatch, "invalid_commit_identity")
	}
	args := []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "commit-tree", treeSHA}
	if parentSHA != "" {
		args = append(args, "-p", parentSHA)
	}
	args = append(args, "-m", message)
	commitEnv := append([]string(nil), env...)
	commitEnv = append(commitEnv,
		fmt.Sprintf("GIT_AUTHOR_NAME=%s", authorName),
		fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", authorEmail),
		fmt.Sprintf("GIT_AUTHOR_DATE=%d", authorDate),
		fmt.Sprintf("GIT_COMMITTER_NAME=%s", authorName),
		fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", authorEmail),
		fmt.Sprintf("GIT_COMMITTER_DATE=%d", authorDate),
	)
	return Command{Path: gitPath, Args: args, Env: commitEnv, Dir: dir}, nil
}
