// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"fmt"
	"strings"
)

// GeneratedFileChange is one structured per-file change record between the
// recorded source commit and the generated unsigned commit, produced by
// `git diff-tree`. It intentionally carries blob SHAs and modes rather
// than a raw text patch: validate.go's rules operate on typed fields
// (path, kind, mode, blob identity), not on parsing hunks.
type GeneratedFileChange struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"` // "added", "modified", "deleted", "renamed", "copied", "type_changed"
	OldPath     string `json:"old_path,omitempty"`
	OldBlobSHA  string `json:"old_blob_sha,omitempty"`
	NewBlobSHA  string `json:"new_blob_sha,omitempty"`
	OldFileMode string `json:"old_file_mode,omitempty"`
	NewFileMode string `json:"new_file_mode,omitempty"`
}

// GenerationOutput is generate.go's complete, bounded result: enough
// evidence for validate.go to judge the generated tree without ever
// re-running the tagger or executing anything from the generated tree.
//
// GenerationOutput never carries a Signer, write token, or any other
// credential type; see generate_test.go's TestGenerateNeverAcceptsCredentialSeam
// for a construction-level proof (G04).
type GenerationOutput struct {
	SourceSHA       string                `json:"source_sha"`
	CommitSHA       string                `json:"commit_sha"`
	TreeSHA         string                `json:"tree_sha"`
	ParentSHAs      []string              `json:"parent_shas"`
	Branch          string                `json:"branch"`
	ResolvedVersion string                `json:"resolved_version"`
	Changes         []GeneratedFileChange `json:"changes"`
	TaggerManifest  map[string]any        `json:"tagger_manifest"`
}

// GenerateInput is everything generate.go needs to produce an unsigned
// release commit in an isolated local checkout. It never includes a
// Signer, write-capable GitHub client, or any token/credential type: the
// generate job's credential boundary (§13.6) is enforced by this
// function's signature, not by a runtime check.
type GenerateInput struct {
	// SourceRemotePath is a local filesystem path to a bare Git repository
	// containing SourceSHA. Tests use a fixture built the same way
	// state_git_test.go's newBareFixtureRemote builds the state store's
	// fixture remote; there is no real GitHub remote in this step.
	SourceRemotePath string
	SourceSHA        string
	// TargetBranch is the exact branch name Generate checks out
	// SourceSHA onto before invoking the tagger. Generate is a
	// single-branch/single-version primitive: it never derives this from
	// a command or decides between a release/development branch itself.
	// A full prepare operation (cut a new release branch from a dev
	// source, then separately advance the dev branch to its next
	// development version) is two independent Generate calls with two
	// independent SourceSHA/TargetBranch/ResolvedVersion triples, each
	// producing its own unsigned commit; sequencing those calls is the
	// orchestration layer's job (B08/B09), using version.go's
	// VersionResolution.ReleaseBranch/DevelopmentBranch to name each call's
	// TargetBranch. Reuse releaseBranchName/devBranchName (version.go) to
	// compute this value; Generate does not recompute it.
	TargetBranch    string
	ResolvedVersion string
	UntaggedModules []string
	ExcludedModules []string
	ExcludedDirs    []string
	// TaggerBinaryPath is a path to an already-built tagger executable.
	// generate.go never builds it and never imports the tagger's package
	// main; callers build it once (tests use buildTaggerBinary in
	// generate_test.go) from the trusted repository revision, per §13.6.
	TaggerBinaryPath string
	// WorkDir is a directory the caller owns exclusively (tests use
	// t.TempDir()). generate.go creates a fresh checkout inside it and
	// never imports another job's `.git` directory.
	WorkDir string
	// CacheDirs are isolated GOPATH/GOCACHE/GOMODCACHE locations for this
	// generation attempt. Every caller that invokes the real tagger binary
	// must supply fresh, exclusively-owned directories here; generate.go
	// never falls back to the ambient process environment's cache.
	CacheDirs TaggerCacheDirs
}

// Generate builds an unsigned release commit in an isolated local
// checkout and returns bounded evidence about it. It never mutates the
// caller's SourceRemotePath (it only fetches from it), never pushes
// anywhere, and never signs anything: signing is B08's responsibility
// using a completely separate call to Seal, never reachable from this
// function.
func Generate(ctx context.Context, runner CommandRunner, input GenerateInput) (GenerationOutput, error) {
	if !ValidGitObjectID(input.SourceSHA) {
		return GenerationOutput{}, typedContractError("invalid_git_object")
	}
	branch := input.TargetBranch
	if branch == "" {
		return GenerationOutput{}, typedContractError("missing_target_branch")
	}
	g := &generator{ctx: ctx, runner: runner, workDir: input.WorkDir}

	if _, err := g.run("init", "--quiet", "-b", branch); err != nil {
		return GenerationOutput{}, err
	}
	if _, err := g.run("fetch", "--quiet", input.SourceRemotePath, input.SourceSHA); err != nil {
		return GenerationOutput{}, wrapReleaseError(ErrorClassGenerationFailed, "generation_fetch_failed", err)
	}
	if _, err := g.run("checkout", "--quiet", "-B", branch, input.SourceSHA); err != nil {
		return GenerationOutput{}, wrapReleaseError(ErrorClassGenerationFailed, "generation_checkout_failed", err)
	}
	if err := validateLocalReplacements(input.WorkDir, input.ExcludedDirs); err != nil {
		return GenerationOutput{}, err
	}

	// Fixed, unsigned, non-secret committer identity. commit.gpgsign and
	// tag.gpgsign are explicitly disabled per-invocation (not merely left
	// at their default) so this checkout can never accidentally produce a
	// Git-level signature even if the ambient environment has global
	// signing configured; content-layer signing happens later, in B08,
	// through Seal/Open, never here.
	if _, err := g.run("config", "user.name", generationCommitterName); err != nil {
		return GenerationOutput{}, err
	}
	if _, err := g.run("config", "user.email", generationCommitterEmail); err != nil {
		return GenerationOutput{}, err
	}
	if _, err := g.run("config", "commit.gpgsign", "false"); err != nil {
		return GenerationOutput{}, err
	}
	if _, err := g.run("config", "tag.gpgsign", "false"); err != nil {
		return GenerationOutput{}, err
	}

	if err := runTaggerGeneration(ctx, runner, input); err != nil {
		return GenerationOutput{}, err
	}

	commitSHA, err := g.trimmedOutput("rev-parse", "HEAD")
	if err != nil {
		return GenerationOutput{}, err
	}
	treeSHA, err := g.trimmedOutput("rev-parse", commitSHA+"^{tree}")
	if err != nil {
		return GenerationOutput{}, err
	}
	parents, err := g.commitParents(commitSHA)
	if err != nil {
		return GenerationOutput{}, err
	}
	changes, err := g.diffTree(input.SourceSHA, commitSHA)
	if err != nil {
		return GenerationOutput{}, err
	}
	manifest, err := readTaggerManifest(input.WorkDir)
	if err != nil {
		return GenerationOutput{}, err
	}

	return GenerationOutput{
		SourceSHA:       input.SourceSHA,
		CommitSHA:       commitSHA,
		TreeSHA:         treeSHA,
		ParentSHAs:      parents,
		Branch:          branch,
		ResolvedVersion: input.ResolvedVersion,
		Changes:         changes,
		TaggerManifest:  manifest,
	}, nil
}

const (
	generationCommitterName  = "gardener-release-generate"
	generationCommitterEmail = "gardener-release-generate@datadoghq.invalid"
)

type generator struct {
	ctx     context.Context
	runner  CommandRunner
	workDir string
}

func (g *generator) run(args ...string) (CommandResult, error) {
	fixedArgs := append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null"}, args...)
	result, err := g.runner.Run(g.ctx, Command{
		Path: "git",
		Args: fixedArgs,
		Env:  gitStateEnv(),
		Dir:  g.workDir,
	})
	if err != nil {
		return result, wrapReleaseError(ErrorClassGenerationFailed, "generation_git_failed", err)
	}
	return result, nil
}

func (g *generator) trimmedOutput(args ...string) (string, error) {
	result, err := g.run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (g *generator) commitParents(commitSHA string) ([]string, error) {
	out, err := g.trimmedOutput("rev-list", "--parents", "-n", "1", commitSHA)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return nil, typedGenerationError("generation_commit_missing")
	}
	return fields[1:], nil
}

// diffTree returns a structured per-file change list between fromSHA and
// toSHA using `git diff-tree --raw`, which reports mode, blob SHA, and
// change kind per path without ever checking out or executing anything
// in either tree.
func (g *generator) diffTree(fromSHA, toSHA string) ([]GeneratedFileChange, error) {
	out, err := g.trimmedOutput("diff-tree", "-r", "--raw", "-z", "-M", "-C", fromSHA, toSHA)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return parseDiffTreeRaw(out)
}

// parseDiffTreeRaw parses `git diff-tree -r --raw -z` output. Each record
// is ":oldmode newmode oldsha newsha status\0path\0[newpath\0]", NUL
// separated because `-z` disables path quoting/escaping; a path with
// unusual bytes still round-trips exactly rather than being reinterpreted
// as escape sequences.
func parseDiffTreeRaw(raw string) ([]GeneratedFileChange, error) {
	tokens := strings.Split(raw, "\x00")
	var changes []GeneratedFileChange
	i := 0
	for i < len(tokens) {
		record := tokens[i]
		if record == "" {
			i++
			continue
		}
		if !strings.HasPrefix(record, ":") {
			return nil, typedGenerationError("generation_diff_unparseable")
		}
		fields := strings.Fields(record)
		if len(fields) != 5 {
			return nil, typedGenerationError("generation_diff_unparseable")
		}
		oldMode, newMode, oldSHA, newSHA, statusField := fields[0], fields[1], fields[2], fields[3], fields[4]
		oldMode = strings.TrimPrefix(oldMode, ":")
		status := statusField[:1]
		i++
		if i >= len(tokens) {
			return nil, typedGenerationError("generation_diff_unparseable")
		}
		path := tokens[i]
		i++
		change := GeneratedFileChange{
			Path:        path,
			OldBlobSHA:  oldSHA,
			NewBlobSHA:  newSHA,
			OldFileMode: oldMode,
			NewFileMode: newMode,
		}
		switch status {
		case "A":
			change.Kind = "added"
		case "M":
			change.Kind = "modified"
		case "D":
			change.Kind = "deleted"
		case "T":
			change.Kind = "type_changed"
		case "R":
			change.Kind = "renamed"
			if i >= len(tokens) {
				return nil, typedGenerationError("generation_diff_unparseable")
			}
			change.OldPath = path
			change.Path = tokens[i]
			i++
		case "C":
			change.Kind = "copied"
			if i >= len(tokens) {
				return nil, typedGenerationError("generation_diff_unparseable")
			}
			change.OldPath = path
			change.Path = tokens[i]
			i++
		default:
			return nil, typedGenerationError("generation_diff_unparseable")
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func typedGenerationError(code string) *ReleaseError {
	return newReleaseError(ErrorClassGenerationFailed, code)
}

func typedGenerationErrorf(code, format string, args ...any) *ReleaseError {
	err := newReleaseError(ErrorClassGenerationFailed, code)
	err.Detail = map[string]string{"message": fmt.Sprintf(format, args...)}
	return err
}
