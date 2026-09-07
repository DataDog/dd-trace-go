// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// stateRecordDocument is the on-disk JSON shape for reservation.json,
// combining the immutable reservation with the fields that evolve through
// append-only events (phase, workflow/tool SHA, and the signed-output
// fields added at the `signed` transition). Splitting the wire format this
// way keeps Reservation's Go type honestly immutable while still storing
// everything in one file, matching §13.5's example paths.
type stateRecordDocument struct {
	Reservation  Reservation    `json:"reservation"`
	Phase        OperationPhase `json:"phase"`
	WorkflowSHA  string         `json:"workflow_sha,omitempty"`
	ToolSHA      string         `json:"tool_sha,omitempty"`
	SignedOutput *SignedOutput  `json:"signed_output,omitempty"`
}

// GitStateStore implements StateStore against a state branch fetched from
// and pushed to a fixed local remote path, using the existing Git seam
// (CommandRunner) for every subprocess call. It never talks to a real
// GitHub remote: RemotePath is a local filesystem path to a bare
// repository, supplied by the caller (tests use a fixture; there is no
// production wiring in this step).
//
// GitStateStore treats a missing state branch as a hard, fail-closed
// error (S01): it never creates the branch itself. Tests that need a
// branch to read from must create it out-of-band before constructing the
// store, mirroring how a provisioning error is distinct from "empty of
// this request key."
type GitStateStore struct {
	runner     CommandRunner
	remotePath string
	branch     string
	workDir    string
	signer     Signer
	verifier   Verifier
	trustedKey []byte
	clock      Clock
	committer  CommitIdentity
}

// CommitIdentity is the fixed, non-secret identity used for state-branch
// commits. It is not a signing key; the commit signature comes from the
// injected Signer/trustedKey.
type CommitIdentity struct {
	Name  string
	Email string
}

// NewGitStateStore builds a store bound to one local bare "remote" and one
// working directory used to stage commits before pushing. workDir must be
// a directory the caller owns exclusively (tests use t.TempDir()); the
// store creates a fresh Git repository there and never imports another
// job's `.git` directory, matching §13.6.
func NewGitStateStore(runner CommandRunner, remotePath, branch, workDir string, signer Signer, verifier Verifier, trustedKey []byte, committer CommitIdentity, clock Clock) *GitStateStore {
	if clock == nil {
		clock = RealClock{}
	}
	return &GitStateStore{
		runner:     runner,
		remotePath: remotePath,
		branch:     branch,
		workDir:    workDir,
		signer:     signer,
		verifier:   verifier,
		trustedKey: trustedKey,
		clock:      clock,
		committer:  committer,
	}
}

func (s *GitStateStore) run(ctx context.Context, args ...string) (CommandResult, error) {
	return s.runStdin(ctx, "", args...)
}

func (s *GitStateStore) runStdin(ctx context.Context, stdin string, args ...string) (CommandResult, error) {
	result, err := s.runner.Run(ctx, Command{
		Path:  "git",
		Args:  append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null"}, args...),
		Env:   gitStateEnv(),
		Dir:   s.workDir,
		Stdin: stdin,
	})
	if err != nil {
		return result, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_read_failed", err)
	}
	return result, nil
}

// gitStateEnv reuses the same allowlisted environment shape as git.go's
// safeGitEnv, but explicitly allows local file-protocol access: state.go's
// remote is always a caller-supplied local path (never a URL), so
// protocol.file.allow is set per-invocation above rather than through the
// shared read-only ShowObject environment.
func gitStateEnv() []string {
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":      "/dev/null",
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_CONFIG_SYSTEM":      "/dev/null",
		"GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_OPTIONAL_LOCKS":     "0",
		"GIT_TERMINAL_PROMPT":    "0",
		"GOTOOLCHAIN":            "local",
		"GOWORK":                 "off",
		"LANG":                   "C.UTF-8",
		"LC_ALL":                 "C.UTF-8",
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

// InitWorkingRepository creates a fresh, empty Git repository at workDir.
// Callers must call this once before using a GitStateStore backed by a
// new workDir; it never imports another job's `.git` directory.
func (s *GitStateStore) InitWorkingRepository(ctx context.Context) error {
	if _, err := s.run(ctx, "init", "--quiet", "-b", "work"); err != nil {
		return err
	}
	return nil
}

// fetchBranchHead fetches s.branch from the remote into a local tracking
// ref and returns its commit SHA. It returns a state_conflict-classed
// "state_branch_missing" error if the branch does not exist on the
// remote: a missing state branch is a provisioning error, never grounds
// to initialize one (S01).
func (s *GitStateStore) fetchBranchHead(ctx context.Context) (string, error) {
	result, err := s.run(ctx, "ls-remote", "--exit-code", s.remotePath, "refs/heads/"+s.branch)
	if err != nil || strings.TrimSpace(result.Stdout) == "" {
		return "", newReleaseError(ErrorClassStateConflict, "state_branch_missing")
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) < 1 || !ValidGitObjectID(fields[0]) {
		return "", newReleaseError(ErrorClassStateConflict, "state_branch_missing")
	}
	head := fields[0]
	if _, err := s.run(ctx, "fetch", "--quiet", s.remotePath, s.branch); err != nil {
		return "", err
	}
	return head, nil
}

// readTreeFile reads one path's blob content at the given commit, or
// returns found=false if the path does not exist in that tree.
func (s *GitStateStore) readTreeFile(ctx context.Context, commitSHA, relPath string) ([]byte, bool, error) {
	_, existsErr := s.runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"cat-file", "-e", commitSHA + ":" + relPath},
		Env:  gitStateEnv(),
		Dir:  s.workDir,
	})
	if existsErr != nil {
		// git cat-file -e exits non-zero when the path is absent from the
		// tree. That is expected and not itself evidence-incomplete; only a
		// failure on the following read (which requires existence to have
		// already been proven) is reported as an error.
		return nil, false, nil
	}
	out, err := s.run(ctx, "show", commitSHA+":"+relPath)
	if err != nil {
		return nil, false, err
	}
	return []byte(out.Stdout), true, nil
}

// listTreePaths lists every blob path under dirPath at the given commit,
// or returns an empty slice if dirPath does not exist.
func (s *GitStateStore) listTreePaths(ctx context.Context, commitSHA, dirPath string) ([]string, error) {
	if _, err := s.runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"cat-file", "-e", commitSHA + ":" + dirPath},
		Env:  gitStateEnv(),
		Dir:  s.workDir,
	}); err != nil {
		// Absent directory in the tree: same not-an-error rationale as
		// readTreeFile's existence probe.
		return nil, nil
	}
	result, err := s.run(ctx, "ls-tree", "-r", "--name-only", commitSHA, dirPath)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(result.Stdout)
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// loadRecordAt loads and verifies one request key's record from the tree
// at commitSHA. It returns found=false only when reservation.json is
// absent; any structural or signature failure is an error.
func (s *GitStateStore) loadRecordAt(ctx context.Context, commitSHA, requestKey string) (Record, bool, error) {
	parts := strings.SplitN(requestKey, ":", 2)
	if len(parts) != 2 {
		return Record{}, false, newReleaseError(ErrorClassContractMismatch, "invalid_request_key")
	}
	paths, err := StatePaths(parts[0], parts[1])
	if err != nil {
		return Record{}, false, err
	}
	raw, found, err := s.readTreeFile(ctx, commitSHA, paths.Reservation)
	if err != nil {
		return Record{}, false, err
	}
	if !found {
		return Record{}, false, nil
	}
	var envelope SignedEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Record{}, false, newReleaseError(ErrorClassStateConflict, "invalid_state_record")
	}
	data, err := Open(s.verifier, envelope, s.trustedKey)
	if err != nil {
		return Record{}, false, err
	}
	var doc stateRecordDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return Record{}, false, newReleaseError(ErrorClassStateConflict, "invalid_state_record")
	}
	if !KnownPhase(doc.Phase) {
		return Record{}, false, newReleaseError(ErrorClassStateConflict, "unknown_operation_phase")
	}
	eventPaths, err := s.listTreePaths(ctx, commitSHA, paths.EventsDir)
	if err != nil {
		return Record{}, false, err
	}
	sort.Strings(eventPaths)
	events := make([]Event, 0, len(eventPaths))
	for _, eventPath := range eventPaths {
		eventRaw, found, err := s.readTreeFile(ctx, commitSHA, eventPath)
		if err != nil {
			return Record{}, false, err
		}
		if !found {
			return Record{}, false, newReleaseError(ErrorClassStateConflict, "missing_event_file")
		}
		var eventEnvelope SignedEnvelope
		if err := json.Unmarshal(eventRaw, &eventEnvelope); err != nil {
			return Record{}, false, newReleaseError(ErrorClassStateConflict, "invalid_state_record")
		}
		eventData, err := Open(s.verifier, eventEnvelope, s.trustedKey)
		if err != nil {
			return Record{}, false, err
		}
		var event Event
		if err := json.Unmarshal(eventData, &event); err != nil {
			return Record{}, false, newReleaseError(ErrorClassStateConflict, "invalid_state_record")
		}
		events = append(events, event)
	}
	if err := VerifyEventChain(requestKey, events); err != nil {
		return Record{}, false, err
	}
	if doc.Reservation.RequestKey != requestKey {
		return Record{}, false, newReleaseError(ErrorClassStateConflict, "request_key_mismatch")
	}
	return Record{
		Reservation:  doc.Reservation,
		Phase:        doc.Phase,
		WorkflowSHA:  doc.WorkflowSHA,
		ToolSHA:      doc.ToolSHA,
		SignedOutput: doc.SignedOutput,
		Events:       events,
	}, true, nil
}

func (s *GitStateStore) LoadState(ctx context.Context, requestKey string) (LoadResult, error) {
	head, err := s.fetchBranchHead(ctx)
	if err != nil {
		return LoadResult{}, err
	}
	record, found, err := s.loadRecordAt(ctx, head, requestKey)
	if err != nil {
		return LoadResult{}, err
	}
	return LoadResult{Record: record, Found: found, RemoteHead: head}, nil
}

func (s *GitStateStore) IncompleteOperationsOnLine(ctx context.Context, releaseLine string) ([]Record, error) {
	head, err := s.fetchBranchHead(ctx)
	if err != nil {
		return nil, err
	}
	repoDirs, err := s.listTreePaths(ctx, head, "requests")
	if err != nil {
		return nil, err
	}
	requestKeys := map[string]bool{}
	for _, filePath := range repoDirs {
		if !strings.HasSuffix(filePath, "/reservation.json") {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(filePath, "requests/"), "/")
		segments := strings.Split(relative, "/")
		if len(segments) < 3 {
			continue
		}
		requestKeys[segments[0]+":"+segments[1]] = true
	}
	keys := make([]string, 0, len(requestKeys))
	for key := range requestKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record, found, err := s.loadRecordAt(ctx, head, key)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newReleaseError(ErrorClassStateConflict, "missing_reservation_file")
		}
		if record.Reservation.ReleaseLine != releaseLine || record.Phase == PhaseComplete {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// PersistReservation writes decision.Record as one new commit on the
// state branch, built from a tree that starts at expectedParent's tree
// (so unrelated request keys are preserved) and replaces only this
// request key's files. It pushes with a fast-forward-only push whose
// refspec names expectedParent as the old value, so a concurrently
// advanced branch is rejected rather than overwritten (S02).
func (s *GitStateStore) PersistReservation(ctx context.Context, decision ReservationDecision, expectedParent string) (string, error) {
	if s.signer == nil {
		return "", newReleaseError(ErrorClassEvidenceIncomplete, "signer_unavailable")
	}
	requestKey := decision.Record.Reservation.RequestKey
	parts := strings.SplitN(requestKey, ":", 2)
	if len(parts) != 2 {
		return "", newReleaseError(ErrorClassContractMismatch, "invalid_request_key")
	}
	paths, err := StatePaths(parts[0], parts[1])
	if err != nil {
		return "", err
	}

	// The state branch must already exist (S01: a missing branch is a
	// provisioning error, never bootstrapped here) and its actual current
	// head must match the caller's expectedParent. A caller-supplied empty
	// expectedParent is only honest when the branch is genuinely still
	// empty of this request key; it is never a way to skip verifying the
	// branch exists.
	actualHead, err := s.fetchBranchHead(ctx)
	if err != nil {
		return "", err
	}
	if actualHead != expectedParent {
		return "", newReleaseError(ErrorClassStateConflict, "state_conflict")
	}

	baseTree := ""
	if expectedParent != "" {
		treeResult, err := s.run(ctx, "rev-parse", expectedParent+"^{tree}")
		if err != nil {
			return "", newReleaseError(ErrorClassStateConflict, "state_conflict")
		}
		baseTree = strings.TrimSpace(treeResult.Stdout)
	}

	doc := stateRecordDocument{
		Reservation:  decision.Record.Reservation,
		Phase:        decision.Record.Phase,
		WorkflowSHA:  decision.Record.WorkflowSHA,
		ToolSHA:      decision.Record.ToolSHA,
		SignedOutput: decision.Record.SignedOutput,
	}
	docBytes, err := json.Marshal(doc)
	if err != nil {
		return "", wrapReleaseError(ErrorClassStateConflict, "state_marshal_failed", err)
	}
	reservationEnvelope, err := Seal(s.signer, docBytes)
	if err != nil {
		return "", err
	}
	reservationBlob, err := json.Marshal(reservationEnvelope)
	if err != nil {
		return "", wrapReleaseError(ErrorClassStateConflict, "state_marshal_failed", err)
	}

	writes := map[string][]byte{paths.Reservation: reservationBlob}
	for _, event := range decision.Record.Events {
		eventBytes, err := json.Marshal(event)
		if err != nil {
			return "", wrapReleaseError(ErrorClassStateConflict, "state_marshal_failed", err)
		}
		eventEnvelope, err := Seal(s.signer, eventBytes)
		if err != nil {
			return "", err
		}
		eventBlob, err := json.Marshal(eventEnvelope)
		if err != nil {
			return "", wrapReleaseError(ErrorClassStateConflict, "state_marshal_failed", err)
		}
		writes[paths.EventPath(event.Sequence)] = eventBlob
	}

	newTree, err := s.writeTree(ctx, baseTree, writes)
	if err != nil {
		return "", err
	}
	commitSHA, err := s.commitTree(ctx, newTree, expectedParent, fmt.Sprintf("state: reserve %s", requestKey))
	if err != nil {
		return "", err
	}
	if err := s.pushFastForward(ctx, commitSHA, expectedParent); err != nil {
		return "", err
	}
	return commitSHA, nil
}

// writeTree builds a new tree object starting from baseTree (or an empty
// tree if baseTree is "") and applying writes, using `git hash-object` and
// `git mktree` plumbing rather than a working-directory checkout. Building
// trees this way keeps unrelated request keys' files untouched even
// though this process never checks out the whole branch.
func (s *GitStateStore) writeTree(ctx context.Context, baseTree string, writes map[string][]byte) (string, error) {
	entries := map[string]string{}
	if baseTree != "" {
		result, err := s.run(ctx, "ls-tree", "-r", baseTree)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(strings.TrimRight(result.Stdout, "\n"), "\n") {
			if line == "" {
				continue
			}
			tab := strings.Index(line, "\t")
			if tab < 0 {
				continue
			}
			meta := strings.Fields(line[:tab])
			if len(meta) < 3 {
				continue
			}
			entries[line[tab+1:]] = meta[2]
		}
	}
	for relPath, content := range writes {
		blobSHA, err := s.hashObject(ctx, content)
		if err != nil {
			return "", err
		}
		entries[relPath] = blobSHA
	}
	return s.buildTreeLevel(ctx, entries, "")
}

// buildTreeLevel builds one tree object for the directory prefix ("" for
// the root) out of entries, a flat map of every blob path to its blob SHA
// anywhere in the whole tree. `git mktree` only accepts single-level,
// non-recursive entries (a path containing a slash is rejected), so this
// recurses depth-first: every subdirectory directly under prefix is built
// into its own tree object first, then this level's mktree call reuses
// those subtree SHAs as tree entries alongside this level's own blobs.
func (s *GitStateStore) buildTreeLevel(ctx context.Context, entries map[string]string, prefix string) (string, error) {
	subdirs := map[string]bool{}
	direct := map[string]string{}
	for relPath, blobSHA := range entries {
		rest := relPath
		if prefix != "" {
			if !strings.HasPrefix(relPath, prefix+"/") {
				continue
			}
			rest = strings.TrimPrefix(relPath, prefix+"/")
		}
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			subdirs[rest[:slash]] = true
			continue
		}
		direct[rest] = blobSHA
	}
	names := make([]string, 0, len(direct)+len(subdirs))
	subtreeSHAs := map[string]string{}
	for name := range subdirs {
		childPrefix := name
		if prefix != "" {
			childPrefix = prefix + "/" + name
		}
		subtreeSHA, err := s.buildTreeLevel(ctx, entries, childPrefix)
		if err != nil {
			return "", err
		}
		subtreeSHAs[name] = subtreeSHA
		names = append(names, name)
	}
	for name := range direct {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		if subtreeSHA, ok := subtreeSHAs[name]; ok {
			builder.WriteString(fmt.Sprintf("040000 tree %s\t%s\n", subtreeSHA, name))
			continue
		}
		builder.WriteString(fmt.Sprintf("100644 blob %s\t%s\n", direct[name], name))
	}
	result, err := s.runStdin(ctx, builder.String(), "mktree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (s *GitStateStore) hashObject(ctx context.Context, content []byte) (string, error) {
	result, err := s.runStdin(ctx, string(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (s *GitStateStore) commitTree(ctx context.Context, treeSHA, parentSHA, message string) (string, error) {
	authorDate := s.clock.Now().Unix()
	command, err := BuildCommitTreeCommand("git", gitStateEnv(), s.workDir, treeSHA, parentSHA, message, s.committer.Name, s.committer.Email, authorDate)
	if err != nil {
		return "", err
	}
	result, err := s.runner.Run(ctx, command)
	if err != nil {
		return "", wrapReleaseError(ErrorClassEvidenceIncomplete, "git_commit_failed", err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// pushFastForward pushes commitSHA to s.branch on the remote, using
// --force-with-lease with expectedParent as the exact expected old value.
// An empty expectedParent means the branch must not already have this
// request key's content; GitStateStore only uses that case when the
// caller already fetched a head and is layering onto its tree, so an
// empty expectedParent here still names the true current head captured by
// PersistReservation's caller through LoadResult.RemoteHead. The push is
// never `--force`, never a mirror/tags/all push, and never touches a ref
// other than the fixed state branch.
func (s *GitStateStore) pushFastForward(ctx context.Context, commitSHA, expectedParent string) error {
	lease := fmt.Sprintf("--force-with-lease=refs/heads/%s:%s", s.branch, expectedParent)
	refspec := commitSHA + ":refs/heads/" + s.branch
	if _, err := s.run(ctx, "push", lease, s.remotePath, refspec); err != nil {
		return newReleaseError(ErrorClassStateConflict, "state_conflict")
	}
	return nil
}
