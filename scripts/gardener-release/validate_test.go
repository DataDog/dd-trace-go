// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// mutateAndAmend rewrites path's blob content at HEAD directly through
// Git plumbing (hash-object/mktree/commit-tree/update-ref) rather than
// checking out and re-running the tagger: the task requires building
// hostile fixtures this way, so G01/G02/G03's forbidden changes are
// deliberately crafted, not accidental tagger output. It returns the
// amended commit SHA.
func mutateAndAmend(t *testing.T, dir, commitSHA string, mutate func(entries map[string]treeEntry)) string {
	t.Helper()
	entries := readFullTree(t, dir, commitSHA)
	mutate(entries)
	treeSHA := writeFullTree(t, dir, entries)
	parent := runGit(t, dir, "rev-list", "--parents", "-n", "1", commitSHA)
	fields := strings.Fields(parent)
	if len(fields) < 2 {
		t.Fatalf("commit %s has no parent to preserve", commitSHA)
	}
	out := runGitWithIdentity(t, dir, "commit-tree", treeSHA, "-p", fields[1], "-m", "hostile mutation")
	return strings.TrimSpace(out)
}

type treeEntry struct {
	mode string
	kind string // "blob" or "tree" (this helper only ever stores flat blobs, so "tree" is unused but kept for clarity)
	sha  string
}

func readFullTree(t *testing.T, dir, commitSHA string) map[string]treeEntry {
	t.Helper()
	out := runGit(t, dir, "ls-tree", "-r", commitSHA)
	entries := map[string]treeEntry{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		tab := strings.Index(line, "\t")
		meta := strings.Fields(line[:tab])
		entries[line[tab+1:]] = treeEntry{mode: meta[0], kind: meta[1], sha: meta[2]}
	}
	return entries
}

func writeFullTree(t *testing.T, dir string, entries map[string]treeEntry) string {
	t.Helper()
	// Build depth-first using the same approach as state_git.go's
	// buildTreeLevel, but standalone here since validate_test.go's
	// fixtures do not go through GitStateStore.
	var build func(prefix string) string
	build = func(prefix string) string {
		subdirs := map[string]bool{}
		direct := map[string]treeEntry{}
		for path, entry := range entries {
			rest := path
			if prefix != "" {
				if !strings.HasPrefix(path, prefix+"/") {
					continue
				}
				rest = strings.TrimPrefix(path, prefix+"/")
			}
			if slash := strings.IndexByte(rest, '/'); slash >= 0 {
				subdirs[rest[:slash]] = true
				continue
			}
			direct[rest] = entry
		}
		var lines []string
		for name := range subdirs {
			childPrefix := name
			if prefix != "" {
				childPrefix = prefix + "/" + name
			}
			subtreeSHA := build(childPrefix)
			lines = append(lines, "040000 tree "+subtreeSHA+"\t"+name)
		}
		for name, entry := range direct {
			objectType := "blob"
			if entry.mode == "160000" {
				// A gitlink (submodule pointer) entry's "sha" is a commit ID
				// in the linked repository, not a blob in this one; `git
				// mktree` requires the matching object type even though it
				// never actually dereferences it for a gitlink.
				objectType = "commit"
			}
			lines = append(lines, entry.mode+" "+objectType+" "+entry.sha+"\t"+name)
		}
		out := runGitStdin(t, dir, strings.Join(lines, "\n")+"\n", "mktree")
		return strings.TrimSpace(out)
	}
	return build("")
}

func hashBlob(t *testing.T, dir, content string) string {
	t.Helper()
	return strings.TrimSpace(runGitStdin(t, dir, content, "hash-object", "-w", "--stdin"))
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitStateEnv(), "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func runGitStdin(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitStateEnv(), "HOME="+dir)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func runGitWithIdentity(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitStateEnv(),
		"HOME="+dir,
		"GIT_AUTHOR_NAME=hostile",
		"GIT_AUTHOR_EMAIL=hostile@example.com",
		"GIT_AUTHOR_DATE=1700000000",
		"GIT_COMMITTER_NAME=hostile",
		"GIT_COMMITTER_EMAIL=hostile@example.com",
		"GIT_COMMITTER_DATE=1700000000",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// diffTreeChanges reruns diff-tree between fromSHA and toSHA using the
// same production helper Generate uses, so hostile-fixture tests exercise
// exactly the same change-record shape as a real generation.
func diffTreeChanges(t *testing.T, dir, fromSHA, toSHA string) []GeneratedFileChange {
	t.Helper()
	g := &generator{ctx: context.Background(), runner: ExecRunner{}, workDir: dir}
	changes, err := g.diffTree(fromSHA, toSHA)
	if err != nil {
		t.Fatalf("diffTree: %v", err)
	}
	return changes
}

// TestValidateGenerationG01RejectsWorkflowFileChange proves a change to a
// path outside the manifest's permitted output files (modeled here as a
// workflow-shaped file since the fixture has no real .github directory)
// is rejected before any module-content check runs.
func TestValidateGenerationG01RejectsWorkflowFileChange(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	hostileBlob := hashBlob(t, reader.Dir, "on: push\n")
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[".github/workflows/ci.yml"] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "unauthorized_path_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed unauthorized_path_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationG02RejectsExternalDependencyVersionBump proves a
// go.mod edit that changes an *external* (non-manifest) requirement's
// version is rejected, even though the path itself (go.mod) is otherwise
// permitted.
func TestValidateGenerationG02RejectsExternalDependencyVersionBump(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	moduleAGoMod := "moduleA/go.mod"
	moduleAData, found, err := reader.Read(context.Background(), output.CommitSHA, moduleAGoMod)
	if err != nil || !found {
		t.Fatalf("read moduleA go.mod: found=%v err=%v", found, err)
	}
	hostileModData := string(moduleAData) + "\nrequire external.example.com/pkg v9.9.9\n"
	hostileBlob := hashBlob(t, reader.Dir, hostileModData)
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[moduleAGoMod] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "require_added" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed require_added", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationG02RejectsClassificationChange proves flipping an
// existing internal requirement's indirect classification is rejected
// even though its version and path are otherwise manifest-approved.
func TestValidateGenerationG02RejectsClassificationChange(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	moduleBGoMod := "moduleB/go.mod"
	data, found, err := reader.Read(context.Background(), output.CommitSHA, moduleBGoMod)
	if err != nil || !found {
		t.Fatalf("read moduleB go.mod: found=%v err=%v", found, err)
	}
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten strings.Builder
	rewritten.WriteString("module " + mod.ModulePath + "\n\ngo " + mod.Go + "\n\nrequire (\n")
	for _, req := range mod.Require {
		suffix := ""
		if req.Path == "example.com/root/v2" {
			suffix = " // indirect"
		}
		rewritten.WriteString("\t" + req.Path + " " + req.Version + suffix + "\n")
	}
	rewritten.WriteString(")\n\n")
	for _, rep := range mod.Replace {
		rewritten.WriteString("replace " + rep.OldPath + " => " + rep.NewPath + "\n\n")
	}
	hostileBlob := hashBlob(t, reader.Dir, rewritten.String())
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[moduleBGoMod] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "requirement_classification_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed requirement_classification_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationRejectsGodebugDirectiveChange proves a `godebug`
// directive change on an otherwise-permitted go.mod path is rejected,
// matching the guardrail that "Module paths, Go/toolchain directives,
// replacements, exclusions, and retractions remain unchanged" (godebug is
// the same kind of build-affecting directive and must be held to the
// same invariant; several real go.mod files in this repository already
// carry one, so this is not a hypothetical directive).
func TestValidateGenerationRejectsGodebugDirectiveChange(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	moduleAGoMod := "moduleA/go.mod"
	data, found, err := reader.Read(context.Background(), output.CommitSHA, moduleAGoMod)
	if err != nil || !found {
		t.Fatalf("read moduleA go.mod: found=%v err=%v", found, err)
	}
	hostileModData := string(data) + "\ngodebug x509negativeserial=1\n"
	hostileBlob := hashBlob(t, reader.Dir, hostileModData)
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[moduleAGoMod] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "godebug_directive_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed godebug_directive_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationG03RejectsExtraCommit proves a two-parent
// (merge-shaped) or otherwise-modified commit is rejected via the
// single-parent/exact-source-parent check.
func TestValidateGenerationG03RejectsExtraCommit(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	treeSHA := strings.TrimSpace(runGit(t, reader.Dir, "rev-parse", output.CommitSHA+"^{tree}"))
	extraCommit := strings.TrimSpace(runGitWithIdentity(t, reader.Dir, "commit-tree", treeSHA, "-p", output.CommitSHA, "-m", "extra unrelated commit"))

	hostileOutput := output
	hostileOutput.CommitSHA = extraCommit
	hostileOutput.ParentSHAs = []string{output.CommitSHA}

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "unrelated_ancestor" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed unrelated_ancestor", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationG03RejectsSymlinkEscapeOnExistingPath proves that
// replacing an existing permitted-path blob with a symlink is rejected. A
// mode change from 100644 to 120000 is reported by diff-tree as a
// "type_changed" record, which validateNoForbiddenChangeKinds rejects
// unconditionally before even inspecting the mode value: any type change
// on any path is disallowed, not merely a symlink specifically, which is
// a strictly stronger guarantee against a path-escape attempt through a
// symlink target.
func TestValidateGenerationG03RejectsSymlinkEscapeOnExistingPath(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	linkBlob := hashBlob(t, reader.Dir, "../../../etc/passwd")
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries["internal/version/version.go"] = treeEntry{mode: "120000", sha: linkBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "forbidden_change_kind" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed forbidden_change_kind", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationG03RejectsFreshSymlinkAddition proves a brand-new
// symlink entry (an "added" change record, not a type change on an
// existing path) is independently caught by the file-mode-prefix check,
// so a symlink cannot slip through by only ever being added, never
// replacing an existing tracked path.
func TestValidateGenerationG03RejectsFreshSymlinkAddition(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	linkBlob := hashBlob(t, reader.Dir, "../../../etc/passwd")
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries["moduleA/evil-link"] = treeEntry{mode: "120000", sha: linkBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "unauthorized_path_changed" && ErrorCode(err) != "forbidden_file_mode" {
		t.Fatalf("error = %q, want unauthorized_path_changed or forbidden_file_mode", ErrorCode(err))
	}
	if ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("class = %q, want generation_failed", ClassOf(err))
	}
}

// TestValidateGenerationG03RejectsSubmodule proves a gitlink entry (file
// mode 160000) is rejected, modeling an attempt to smuggle a submodule
// pointer into the release commit.
func TestValidateGenerationG03RejectsSubmodule(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := readFullTree(t, reader.Dir, output.CommitSHA)
	entries["vendor/evil-submodule"] = treeEntry{mode: "160000", sha: "0123456789abcdef0123456789abcdef01234567"}
	treeSHA := writeFullTree(t, reader.Dir, entries)
	parent := runGit(t, reader.Dir, "rev-list", "--parents", "-n", "1", output.CommitSHA)
	fields := strings.Fields(parent)
	hostileCommit := strings.TrimSpace(runGitWithIdentity(t, reader.Dir, "commit-tree", treeSHA, "-p", fields[1], "-m", "hostile submodule"))

	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "unauthorized_path_changed" && ErrorCode(err) != "forbidden_file_mode" {
		t.Fatalf("error = %q, want unauthorized_path_changed or forbidden_file_mode", ErrorCode(err))
	}
	if ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("class = %q, want generation_failed", ClassOf(err))
	}
}

// TestValidateGenerationG03RejectsUnexpectedFileDeletion proves a deleted
// file anywhere in the changed set is rejected outright.
func TestValidateGenerationG03RejectsUnexpectedFileDeletion(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		delete(entries, "moduleC/go.sum")
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "unexpected_file_deletion" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed unexpected_file_deletion", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationRejectsExecutableModeChange proves a mode flip to
// executable (100755) on an otherwise-permitted path is rejected.
func TestValidateGenerationRejectsExecutableModeChange(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := readFullTree(t, reader.Dir, output.CommitSHA)
	existing := entries["internal/version/version.go"]
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries["internal/version/version.go"] = treeEntry{mode: "100755", sha: existing.sha}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "executable_mode_change" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed executable_mode_change", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationRejectsVersionFileNonTagLineChange proves that
// even a change confined to internal/version/version.go is rejected if
// it touches anything other than the var Tag line.
func TestValidateGenerationRejectsVersionFileNonTagLineChange(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	data, found, err := reader.Read(context.Background(), output.CommitSHA, "internal/version/version.go")
	if err != nil || !found {
		t.Fatalf("read version.go: found=%v err=%v", found, err)
	}
	hostileData := strings.Replace(string(data), "package version", "package version // hostile comment", 1)
	if hostileData == string(data) {
		t.Fatal("fixture does not contain expected package line")
	}
	hostileBlob := hashBlob(t, reader.Dir, hostileData)
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries["internal/version/version.go"] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "version_file_non_tag_line_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed version_file_non_tag_line_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationRejectsAddedReplaceDirective proves that adding a
// brand-new replace directive to make `go mod tidy` succeed (rather than
// relying on a source replacement that was already reviewed) is rejected:
// "Do not add temporary replacements to a generated release commit."
func TestValidateGenerationRejectsAddedReplaceDirective(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	moduleCGoMod := "moduleC/go.mod"
	data, found, err := reader.Read(context.Background(), output.CommitSHA, moduleCGoMod)
	if err != nil || !found {
		t.Fatalf("read moduleC go.mod: found=%v err=%v", found, err)
	}
	hostileData := string(data) + "\nreplace external.example.com/pkg => ./local-shadow\n"
	hostileBlob := hashBlob(t, reader.Dir, hostileData)
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[moduleCGoMod] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "replace_directives_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed replace_directives_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationRejectsInvalidGoSumSyntax proves a malformed
// go.sum line is rejected before its delta is trusted as reviewable data.
func TestValidateGenerationRejectsInvalidGoSumSyntax(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	moduleAGoSum := "moduleA/go.sum"
	if _, found := readFullTree(t, reader.Dir, output.CommitSHA)[moduleAGoSum]; !found {
		t.Skip("moduleA/go.sum not present in this generation; skip malformed-syntax case")
	}
	hostileBlob := hashBlob(t, reader.Dir, "not a valid go.sum line\n")
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[moduleAGoSum] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = ValidateGeneration(context.Background(), reader.Read, hostileOutput, manifest)
	if ErrorCode(err) != "go_sum_invalid_syntax" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed go_sum_invalid_syntax", ClassOf(err), ErrorCode(err))
	}
}

// TestValidateGenerationAcceptsUnchangedVersionFile proves that a
// generation which never touches internal/version/version.go at all
// (modeling the tagger's repair path, which skips rewriting it when a
// prior interrupted run already committed the target version) is not
// misread as a version-file violation. This is a synthetic fixture
// rather than a real tagger repair-path run, since
// validateModuleFiles/validateVersionFileTagLineOnly only need a fake
// modFileReader and a GenerationOutput.Changes list to exercise this
// branch in isolation.
func TestValidateGenerationAcceptsUnchangedVersionFile(t *testing.T) {
	oldModuleAGoMod := "module example.com/root/moduleA/v2\n\ngo 1.26.0\n\nrequire example.com/root/v2 v2.0.0\n\nreplace example.com/root/v2 => ./..\n"
	newModuleAGoMod := "module example.com/root/moduleA/v2\n\ngo 1.26.0\n\nrequire example.com/root/v2 v9.9.9\n\nreplace example.com/root/v2 => ./..\n"
	fakeReader := func(ctx context.Context, commitSHA, path string) ([]byte, bool, error) {
		if path != "moduleA/go.mod" {
			return nil, false, nil
		}
		if commitSHA == "0123456789abcdef0123456789abcdef01234567" {
			return []byte(oldModuleAGoMod), true, nil
		}
		return []byte(newModuleAGoMod), true, nil
	}
	output := GenerationOutput{
		SourceSHA:       "0123456789abcdef0123456789abcdef01234567",
		CommitSHA:       "fedcba9876543210fedcba9876543210fedcba98",
		ResolvedVersion: "v9.9.9",
		ParentSHAs:      []string{"0123456789abcdef0123456789abcdef01234567"},
		Changes: []GeneratedFileChange{
			{Path: "moduleA/go.mod", Kind: "modified", OldFileMode: "100644", NewFileMode: "100644"},
		},
	}
	manifest := PlanManifest{
		SourceSHA:            output.SourceSHA,
		Modules:              []PlanManifestModule{{Path: "example.com/root/v2", Dir: ".", Tagged: true}, {Path: "example.com/root/moduleA/v2", Dir: "moduleA", Tagged: true}},
		PermittedOutputFiles: []string{"internal/version/version.go", "moduleA/go.mod", "moduleA/go.sum"},
		ExpectedTags:         []string{"v9.9.9", "moduleA/v9.9.9"},
	}

	if _, err := validateModuleFiles(context.Background(), fakeReader, output, manifest); err != nil {
		t.Fatalf("validateModuleFiles rejected an unchanged version file: %v", err)
	}
}

// TestValidateGenerationG06RejectsWhenManifestExpectedTagsIsEmpty models
// G06 at validate.go's boundary: a manifest that declares no expected
// tags at all (as would result from a missing required local
// replacement blocking the tagger's own plan computation upstream) must
// never be treated as "nothing to validate" and silently accepted.
func TestValidateGenerationG06RejectsWhenManifestExpectedTagsIsEmpty(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ExpectedTags = nil

	_, err = ValidateGeneration(context.Background(), reader.Read, output, manifest)
	if ErrorCode(err) != "empty_expected_tags" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed empty_expected_tags", ClassOf(err), ErrorCode(err))
	}
}

// TestGenerateG06MissingLocalReplacementBlocksBeforeSigning covers G06 at
// generate.go's own boundary: when the source module lacks a required
// local replace directive for an in-repository dependency, `go mod tidy`
// itself fails (it cannot resolve the dependency through any proxy,
// since GOPROXY=off), so Generate must fail closed rather than produce a
// partial or substitute commit. This negative result is a distinct test
// from G05's positive acceptance case; it does not count as G05 success.
func TestGenerateG06MissingLocalReplacementBlocksBeforeSigning(t *testing.T) {
	remotePath, sourceSHA := sourceFixtureRepoWithBrokenReplace(t, "dev-v2.9.x")
	workDir := t.TempDir()
	_, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: remotePath,
		SourceSHA:        sourceSHA,
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		UntaggedModules:  []string{"example.com/root/moduleC/v2"},
		TaggerBinaryPath: buildTaggerBinary(t),
		WorkDir:          workDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("class = %q, want generation_failed (missing replace must block, not substitute)", ClassOf(err))
	}
}
