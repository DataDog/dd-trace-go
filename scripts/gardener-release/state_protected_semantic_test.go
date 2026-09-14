// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

type protectedSignedStateFixture struct {
	store  *GitStateStore
	record Record
	head   string
}

func newProtectedSignedStateFixture(t *testing.T) protectedSignedStateFixture {
	t.Helper()
	policy, privateKey := ephemeralSSHPolicy(t)
	t.Setenv(ProtectedSSHSigningKeyEnvironment, privateKey)
	key, err := LoadProtectedSSHSigningKey(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := key.Close(); err != nil {
			t.Error(err)
		}
	})
	contentSigner, err := NewSSHContentSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	trustedKey, err := contentSigner.publicWire()
	if err != nil {
		t.Fatal(err)
	}
	remote := newBareFixtureRemote(t)
	seedProtectedStateBranch(t, remote, key)
	store := newGitStateStore(ExecRunner{}, remote, StateBranch, t.TempDir(), contentSigner, contentSigner, trustedKey, CommitIdentity{Name: "gardener-release", Email: "gardener-release@datadoghq.invalid"}, nil, key)
	if err := store.InitWorkingRepository(context.Background()); err != nil {
		t.Fatal(err)
	}

	fingerprintBytes := sha256.Sum256(trustedKey)
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	reservation := baseReservation()
	decision := sealedReservationDecision(t, reservation, fingerprint)
	for index := range decision.Record.Events {
		if decision.Record.Events[index].Kind != EventSigningIntent {
			continue
		}
		var intent GitSigningIntent
		if err := json.Unmarshal(decision.Record.Events[index].Evidence, &intent); err != nil {
			t.Fatal(err)
		}
		intent.Principal = policy.Principal
		intent.Fingerprint = policy.Fingerprint
		decision.Record.Events[index].Evidence = mustJSON(t, intent)
	}
	rechainSemanticEvents(t, &decision.Record)

	loaded, err := store.LoadState(context.Background(), reservation.RequestKey)
	if err != nil || loaded.Found {
		t.Fatalf("load empty protected state: found=%t err=%v", loaded.Found, err)
	}
	reservedHead, err := store.PersistReservation(context.Background(), decision, loaded.RemoteHead)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.LoadState(context.Background(), reservation.RequestKey)
	if err != nil || !reloaded.Found || reloaded.Record.Phase != PhaseReserved {
		t.Fatalf("load protected reservation: %#v err=%v", reloaded, err)
	}

	bundleData := []byte("disposable protected recovery bundle fixture\n")
	bundleDigest := sha256.Sum256(bundleData)
	signed := semanticSignedOutput(reservation)
	signed.SignerFingerprint = fingerprint
	signed.Bundle.SHA256 = hex.EncodeToString(bundleDigest[:])
	signed.Bundle.SizeBytes = int64(len(bundleData))
	signedRecord, alreadySigned, err := AdvanceToSigned(reloaded.Record, signed, signedRecordEvidence(t, signed))
	if err != nil || alreadySigned {
		t.Fatalf("advance protected signed record: already=%t err=%v", alreadySigned, err)
	}
	attestation := sealFixtureAttestation(t, contentSigner, signed)
	signedHead, err := store.PersistSignedRecord(context.Background(), ReservationDecision{Reserved: true, Record: signedRecord}, reservedHead, attestation, bundleData)
	if err != nil {
		t.Fatal(err)
	}
	loadedSigned, err := store.LoadState(context.Background(), reservation.RequestKey)
	if err != nil || !loadedSigned.Found || loadedSigned.Record.Phase != PhaseSigned || loadedSigned.RemoteHead != signedHead {
		t.Fatalf("load protected signed state: %#v err=%v", loadedSigned, err)
	}
	return protectedSignedStateFixture{store: store, record: loadedSigned.Record, head: signedHead}
}

func seedProtectedStateBranch(t *testing.T, remote string, key *ProtectedSSHSigningKey) string {
	t.Helper()
	workDir := t.TempDir()
	runGit(t, workDir, "init", "--quiet", "-b", StateBranch)
	blob := strings.TrimSpace(runGitStdin(t, workDir, "state\n", "hash-object", "-w", "--stdin"))
	tree := strings.TrimSpace(runGitStdin(t, workDir, "100644 blob "+blob+"\t.keep\n", "mktree"))
	command, err := BuildSignedCommitTreeCommand("git", gitStateEnv(), workDir, tree, "", "seed protected state", "gardener-release", "gardener-release@datadoghq.invalid", 1767225600, key)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(runCommand(t, command).Stdout)
	if err := VerifySSHGitSignature(context.Background(), ExecRunner{}, workDir, head, false, key); err != nil {
		t.Fatal(err)
	}
	runGit(t, workDir, "push", "--quiet", remote, head+":refs/heads/"+StateBranch)
	return head
}

func sealFixtureAttestation(t *testing.T, signer Signer, signed SignedOutput) SignedEnvelope {
	t.Helper()
	data, err := json.Marshal(signedAttestation{
		UnsignedSHA: signed.UnsignedSHA, SourceSHA: signed.SourceSHA, TreeSHA: signed.TreeSHA,
		ReleaseSHA: signed.ReleaseSHA, ResolvedVersion: attestationResolvedVersion(signed), ToolDigest: signed.ToolDigest,
		ValidatorDigest: signed.ValidatorDigest, Tags: tagRefNames(signed.Tags),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(signer, data)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func nonCanonicalOpenSSHFingerprint(t *testing.T, canonical string) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	if !strings.HasPrefix(canonical, "SHA256:") || len(canonical) == len("SHA256:") {
		t.Fatalf("invalid canonical fingerprint %q", canonical)
	}
	last := canonical[len(canonical)-1]
	index := strings.IndexByte(alphabet, last)
	if index < 0 || index%4 != 0 || index+1 >= len(alphabet) {
		t.Fatalf("fingerprint %q has unexpected final base64 character", canonical)
	}
	return canonical[:len(canonical)-1] + string(alphabet[index+1])
}

func TestProtectedSSHStateLoadRejectsResealedSemanticMutations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *Record)
	}{
		{
			name: "reservation immutable field",
			mutate: func(_ *testing.T, record *Record) {
				record.Reservation.PolicyRevision = "malformed"
			},
		},
		{
			name: "signing intent fingerprint mismatch",
			mutate: func(t *testing.T, record *Record) {
				rewriteEvent(t, record, EventSigningIntent, func(raw json.RawMessage) json.RawMessage {
					var intent GitSigningIntent
					if err := json.Unmarshal(raw, &intent); err != nil {
						t.Fatal(err)
					}
					intent.Fingerprint = fixtureOpenSSHFingerprint(t, strings.Repeat("9", 64))
					return mustJSON(t, intent)
				})
			},
		},
		{
			name: "non-canonical signing intent fingerprint",
			mutate: func(t *testing.T, record *Record) {
				rewriteEvent(t, record, EventSigningIntent, func(raw json.RawMessage) json.RawMessage {
					var intent GitSigningIntent
					if err := json.Unmarshal(raw, &intent); err != nil {
						t.Fatal(err)
					}
					intent.Fingerprint = nonCanonicalOpenSSHFingerprint(t, intent.Fingerprint)
					return mustJSON(t, intent)
				})
			},
		},
		{
			name: "signed output provenance disagrees with attestation",
			mutate: func(_ *testing.T, record *Record) {
				record.SignedOutput.ToolDigest = strings.Repeat("9", 64)
			},
		},
		{
			name: "causal duplicate signing intent",
			mutate: func(t *testing.T, record *Record) {
				var evidence json.RawMessage
				for _, event := range record.Events {
					if event.Kind == EventSigningIntent {
						evidence = append(json.RawMessage(nil), event.Evidence...)
						break
					}
				}
				if evidence == nil {
					t.Fatal("missing signing intent")
				}
				var err error
				record.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventSigningIntent, evidence)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProtectedSignedStateFixture(t)
			mutated := fixture.record
			test.mutate(t, &mutated)
			rechainSemanticEvents(t, &mutated)
			mutatedHead, err := fixture.store.persistRecord(context.Background(), ReservationDecision{Record: mutated}, fixture.head, nil)
			if err != nil {
				t.Fatalf("persist SSH-signed mutated state: %v", err)
			}
			if err := VerifySSHGitSignature(context.Background(), ExecRunner{}, fixture.store.workDir, mutatedHead, false, fixture.store.gitSigning); err != nil {
				t.Fatalf("mutated state commit lacks authorized SSH signature: %v", err)
			}
			loaded, err := fixture.store.LoadState(context.Background(), mutated.Reservation.RequestKey)
			if err == nil || loaded.Found {
				t.Fatalf("SSH-authenticated semantic mutation loaded: %#v", loaded.Record)
			}
			if ErrorCode(err) == "ssh_git_signature_invalid" || ErrorCode(err) == "ssh_git_signer_mismatch" {
				t.Fatalf("mutation rejected only by Git signature verification: %v", err)
			}
		})
	}
}
