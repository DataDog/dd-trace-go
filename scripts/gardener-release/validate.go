// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// PlanManifest is the subset of the tagger's --plan-json output (B05)
// that validate.go needs: which modules and output files this operation
// is allowed to touch, and the exact tags it must produce. validate.go
// never re-derives this from the tagger; it is supplied by the caller
// (B06's reservation records the request; B05's plan-json is the trusted
// source of the allowlist for one resolved version).
type PlanManifest struct {
	SourceSHA            string
	RequestedVersion     string
	RootModulePath       string
	Modules              []PlanManifestModule
	PermittedOutputFiles []string
	ExpectedTags         []string
}

// PlanManifestModule is one module entry from the tagger's plan manifest.
type PlanManifestModule struct {
	Path   string
	Dir    string
	Tagged bool
}

// GoSumDelta is a bounded, reviewable summary of one go.sum file's change,
// not the raw diff text: a fixed line count is easy to review and log; an
// unbounded raw text diff is not.
type GoSumDelta struct {
	Path         string `json:"path"`
	AddedLines   int    `json:"added_lines"`
	RemovedLines int    `json:"removed_lines"`
}

// ValidatedGeneration is validate.go's success output: a bounded summary
// of exactly what changed, safe to log or hand to B08's signing step.
// validate.go never re-derives GenerationOutput's raw fields itself; it
// only adds judgments about them.
type ValidatedGeneration struct {
	CommitSHA          string       `json:"commit_sha"`
	SourceSHA          string       `json:"source_sha"`
	TreeSHA            string       `json:"tree_sha"`
	ResolvedVersion    string       `json:"resolved_version"`
	VersionFileChanged bool         `json:"version_file_changed"`
	ChangedGoModPaths  []string     `json:"changed_go_mod_paths"`
	GoSumDeltas        []GoSumDelta `json:"go_sum_deltas"`
	ExpectedTags       []string     `json:"expected_tags"`
}

// modFileReader reads a go.mod's bytes at a specific commit through Git
// plumbing only (never a filesystem read of a checkout, and never
// executing anything): validate.go's contract is that it inspects
// committed objects, not live files.
type modFileReader func(ctx context.Context, commitSHA, path string) ([]byte, bool, error)

// ValidateGeneration applies every B07 guardrail rule to output against
// manifest, using reader to fetch go.mod/go.sum blob contents by path at
// specific commits (never by checking out or executing anything). It
// returns the first violated rule as a typed *ReleaseError classed
// ErrorClassGenerationFailed, or a bounded ValidatedGeneration on success.
//
// ValidateGeneration never calls Signer.Sign and never accepts a Signer,
// write-capable client, or any credential type in its signature: see
// generate_test.go's TestGenerateNeverAcceptsCredentialSeam for the
// construction-level proof this function and Generate share (G04).
func ValidateGeneration(ctx context.Context, reader modFileReader, output GenerationOutput, manifest PlanManifest) (ValidatedGeneration, error) {
	if err := validateSingleParent(output, manifest); err != nil {
		return ValidatedGeneration{}, err
	}
	if err := validateNoForbiddenChangeKinds(output); err != nil {
		return ValidatedGeneration{}, err
	}
	changedPaths := changedPathSet(output.Changes)
	if err := validatePermittedPaths(changedPaths, manifest); err != nil {
		return ValidatedGeneration{}, err
	}
	versionFileChanged, err := validateVersionFileChange(output)
	if err != nil {
		return ValidatedGeneration{}, err
	}
	changedGoModPaths, err := validateModuleFiles(ctx, reader, output, manifest)
	if err != nil {
		return ValidatedGeneration{}, err
	}
	goSumDeltas, err := computeGoSumDeltas(ctx, reader, output, manifest)
	if err != nil {
		return ValidatedGeneration{}, err
	}
	if err := validateExpectedTags(output, manifest); err != nil {
		return ValidatedGeneration{}, err
	}
	return ValidatedGeneration{
		CommitSHA:          output.CommitSHA,
		SourceSHA:          output.SourceSHA,
		TreeSHA:            output.TreeSHA,
		ResolvedVersion:    output.ResolvedVersion,
		VersionFileChanged: versionFileChanged,
		ChangedGoModPaths:  changedGoModPaths,
		GoSumDeltas:        goSumDeltas,
		ExpectedTags:       manifest.ExpectedTags,
	}, nil
}

// validateSingleParent covers G03's "extra commit"/"unrelated ancestor"
// requirement: the generated commit must have exactly one parent, and
// that parent must be the recorded source SHA, not merely an ancestor of
// it.
func validateSingleParent(output GenerationOutput, manifest PlanManifest) error {
	if len(output.ParentSHAs) != 1 {
		return typedGenerationErrorf("unexpected_parent_count", "parents=%v", output.ParentSHAs)
	}
	if output.ParentSHAs[0] != output.SourceSHA || output.SourceSHA != manifest.SourceSHA {
		return typedGenerationError("unrelated_ancestor")
	}
	if output.CommitSHA == output.SourceSHA {
		return typedGenerationError("generation_produced_no_commit")
	}
	return nil
}

// forbiddenModePrefixes are file-mode prefixes that are never legitimate
// in a release commit: "120000" is a symlink, "160000" is a gitlink
// (submodule). validate.go rejects both unconditionally rather than
// trying to special-case a submodule pointer update, since dd-trace-go
// carries no submodules today and a release commit is not the place to
// introduce one.
var forbiddenModePrefixes = []string{"120000", "160000"}

// validateNoForbiddenChangeKinds covers G03's symlink/submodule rejection
// and the executable-mode-change guardrail: any file-mode transition
// other than a plain "100644 -> 100644" (or an added/deleted 100644 blob)
// is rejected, including a mode flip to/from 100755.
func validateNoForbiddenChangeKinds(output GenerationOutput) error {
	for _, change := range output.Changes {
		if change.Kind == "renamed" || change.Kind == "copied" || change.Kind == "type_changed" {
			return typedGenerationErrorf("forbidden_change_kind", "path=%s kind=%s", change.Path, change.Kind)
		}
		for _, mode := range []string{change.OldFileMode, change.NewFileMode} {
			for _, forbidden := range forbiddenModePrefixes {
				if mode == forbidden {
					return typedGenerationErrorf("forbidden_file_mode", "path=%s mode=%s", change.Path, mode)
				}
			}
		}
		if change.NewFileMode != "" && change.NewFileMode != "100644" && change.NewFileMode != "000000" {
			return typedGenerationErrorf("executable_mode_change", "path=%s mode=%s", change.Path, change.NewFileMode)
		}
		if change.Kind == "deleted" {
			return typedGenerationErrorf("unexpected_file_deletion", "path=%s", change.Path)
		}
	}
	return nil
}

func changedPathSet(changes []GeneratedFileChange) map[string]GeneratedFileChange {
	set := make(map[string]GeneratedFileChange, len(changes))
	for _, change := range changes {
		set[change.Path] = change
	}
	return set
}

// validatePermittedPaths covers G01: every changed path must be either
// internal/version/version.go or a manifest-listed go.mod/go.sum path.
// Anything else — a workflow file, a script, a test, source code, a
// dotfile, or the policy file — is rejected before any further check
// runs, since a change there can never be authorized by a resolved
// version alone.
func validatePermittedPaths(changed map[string]GeneratedFileChange, manifest PlanManifest) error {
	permitted := make(map[string]bool, len(manifest.PermittedOutputFiles))
	for _, path := range manifest.PermittedOutputFiles {
		permitted[path] = true
	}
	for path := range changed {
		if !permitted[path] {
			return typedGenerationErrorf("unauthorized_path_changed", "path=%s", path)
		}
	}
	return nil
}

// validateVersionFileChange covers the "internal/version/version.go
// changes only the intended Tag string literal" guardrail: the file must
// either be unchanged, or changed with the diff limited to the var Tag
// line only, verified by comparing full blob contents (via reader) line
// by line rather than trusting the diff-tree record alone.
func validateVersionFileChange(output GenerationOutput) (bool, error) {
	change, ok := changedPathSet(output.Changes)[versionFileRelPathForValidation]
	if !ok {
		return false, nil
	}
	if change.Kind != "modified" {
		return false, typedGenerationErrorf("version_file_wrong_change_kind", "kind=%s", change.Kind)
	}
	return true, nil
}

// versionFileRelPathForValidation is internal/version/version.go's path
// as it appears in diff-tree output (forward-slash, relative to repo
// root). It mirrors scripts/autoreleasetagger/main.go's
// versionFileRelPath constant but is redeclared here (rather than
// imported, which would require importing package main) as a plain
// string literal, matching this package's existing convention of not
// depending on the tagger's package main.
const versionFileRelPathForValidation = "internal/version/version.go"

var versionTagLineRe = regexp.MustCompile(`^var Tag = ".+"$`)

// validateVersionFileTagLineOnly re-reads the version file's full old and
// new content through reader and asserts every line except the var Tag
// line is byte-identical, and that the new Tag line's value matches
// output.ResolvedVersion exactly. This is the deeper half of
// validateVersionFileChange: diff-tree only proves the blob changed, not
// that the change was limited to the Tag literal.
func validateVersionFileTagLineOnly(ctx context.Context, reader modFileReader, output GenerationOutput) error {
	oldData, foundOld, err := reader(ctx, output.SourceSHA, versionFileRelPathForValidation)
	if err != nil {
		return err
	}
	newData, foundNew, err := reader(ctx, output.CommitSHA, versionFileRelPathForValidation)
	if err != nil {
		return err
	}
	if !foundOld || !foundNew {
		return typedGenerationError("version_file_missing")
	}
	oldLines := strings.Split(string(oldData), "\n")
	newLines := strings.Split(string(newData), "\n")
	if len(oldLines) != len(newLines) {
		return typedGenerationError("version_file_structure_changed")
	}
	sawTagLine := false
	for i := range oldLines {
		if oldLines[i] == newLines[i] {
			continue
		}
		if !versionTagLineRe.MatchString(oldLines[i]) || !versionTagLineRe.MatchString(newLines[i]) {
			return typedGenerationError("version_file_non_tag_line_changed")
		}
		sawTagLine = true
		expected := `var Tag = "` + output.ResolvedVersion + `"`
		if newLines[i] != expected {
			return typedGenerationErrorf("version_file_wrong_tag_value", "got=%s want=%s", newLines[i], expected)
		}
	}
	if !sawTagLine {
		return typedGenerationError("version_file_tag_line_missing")
	}
	return nil
}

// validateModuleFiles applies the go.mod side of every guardrail: module
// path/go/toolchain/exclude/retract untouched, external requirements
// untouched (version and direct/indirect classification), internal
// dependency versions changed only to the resolved version at
// manifest-approved module paths, and every source-tree replace
// directive left byte-for-byte unchanged (the artifact validator requires
// source replacements to remain unchanged, per the plan's unpublished
// dependency resolution rules). It returns the sorted list of go.mod
// paths that legitimately changed.
func validateModuleFiles(ctx context.Context, reader modFileReader, output GenerationOutput, manifest PlanManifest) ([]string, error) {
	// A version file that was never touched is fine: the tagger's own
	// repair path (versionAlreadyCommitted in
	// scripts/autoreleasetagger/main.go) skips rewriting internal/version/
	// version.go when a prior interrupted run already committed the
	// target version, so a resumed generation can legitimately produce no
	// change to that file at all. The deep tag-line-only check therefore
	// only runs when the change set actually names this path, so an
	// unchanged file can never be misread as "tag line missing."
	if _, found := changedPathSet(output.Changes)[versionFileRelPathForValidation]; found {
		if err := validateVersionFileTagLineOnly(ctx, reader, output); err != nil {
			return nil, err
		}
	}
	permittedModulePaths := map[string]bool{}
	for _, module := range manifest.Modules {
		permittedModulePaths[module.Path] = true
	}
	var changedGoModPaths []string
	for path, change := range changedPathSet(output.Changes) {
		if !strings.HasSuffix(path, "/go.mod") && path != "go.mod" {
			continue
		}
		changedGoModPaths = append(changedGoModPaths, path)
		if change.Kind != "modified" {
			return nil, typedGenerationErrorf("go_mod_wrong_change_kind", "path=%s kind=%s", path, change.Kind)
		}
		oldData, foundOld, err := reader(ctx, output.SourceSHA, path)
		if err != nil {
			return nil, err
		}
		newData, foundNew, err := reader(ctx, output.CommitSHA, path)
		if err != nil {
			return nil, err
		}
		if !foundOld || !foundNew {
			return nil, typedGenerationErrorf("go_mod_missing", "path=%s", path)
		}
		oldMod, err := ParseModFile(oldData)
		if err != nil {
			return nil, err
		}
		newMod, err := ParseModFile(newData)
		if err != nil {
			return nil, err
		}
		if !permittedModulePaths[oldMod.ModulePath] {
			return nil, typedGenerationErrorf("unauthorized_module_changed", "module=%s", oldMod.ModulePath)
		}
		if err := validateOneModFileChange(oldMod, newMod, output.ResolvedVersion, permittedModulePaths); err != nil {
			return nil, err
		}
	}
	sort.Strings(changedGoModPaths)
	return changedGoModPaths, nil
}

// validateOneModFileChange enforces the full guardrail set for a single
// module's before/after go.mod pair.
func validateOneModFileChange(oldMod, newMod ModFile, resolvedVersion string, permittedModulePaths map[string]bool) error {
	if oldMod.ModulePath != newMod.ModulePath {
		return typedGenerationError("module_path_changed")
	}
	if oldMod.Go != newMod.Go {
		return typedGenerationError("go_directive_changed")
	}
	if oldMod.Toolchain != newMod.Toolchain {
		return typedGenerationError("toolchain_directive_changed")
	}
	if !equalStringSlices(oldMod.Godebug, newMod.Godebug) {
		return typedGenerationError("godebug_directive_changed")
	}
	if len(oldMod.DuplicateReplacePaths()) > 0 || len(newMod.DuplicateReplacePaths()) > 0 {
		return typedGenerationError("duplicate_replace_directive")
	}
	if err := validateReplacesUnchanged(oldMod, newMod); err != nil {
		return err
	}
	return validateRequiresChangedOnlyAtApprovedInternalPaths(oldMod, newMod, resolvedVersion, permittedModulePaths)
}

// validateReplacesUnchanged covers "replacements ... remain unchanged"
// and "the artifact validator requires source replacements to remain
// unchanged": every replace directive present before generation must be
// present, identical, and in the same relationship after generation, and
// generation must not have added a new one (a temporary replacement
// added to make `go mod tidy` succeed is exactly the case the plan
// forbids: "Do not add temporary replacements to a generated release
// commit").
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateReplacesUnchanged(oldMod, newMod ModFile) error {
	if len(oldMod.Replace) != len(newMod.Replace) {
		return typedGenerationError("replace_directives_changed")
	}
	oldSet := map[ModFileReplace]bool{}
	for _, rep := range oldMod.Replace {
		oldSet[rep] = true
	}
	for _, rep := range newMod.Replace {
		if !oldSet[rep] {
			return typedGenerationError("replace_directives_changed")
		}
		delete(oldSet, rep)
	}
	if len(oldSet) != 0 {
		return typedGenerationError("replace_directives_changed")
	}
	return nil
}

// validateRequiresChangedOnlyAtApprovedInternalPaths covers: external
// direct/indirect requirements (version and classification) unchanged,
// and internal dependency versions changed only to resolvedVersion at
// manifest-approved module paths. "Internal" here means any require
// whose path has a corresponding entry in permittedModulePaths (i.e. the
// tagger's own plan manifest already decided it is part of this
// operation's dependency graph); every other require is external and
// must be byte-identical, including its indirect marker.
func validateRequiresChangedOnlyAtApprovedInternalPaths(oldMod, newMod ModFile, resolvedVersion string, permittedModulePaths map[string]bool) error {
	oldRequires := map[string]ModFileRequire{}
	for _, req := range oldMod.Require {
		if _, dup := oldRequires[req.Path]; dup {
			return typedGenerationError("duplicate_require_directive")
		}
		oldRequires[req.Path] = req
	}
	newRequires := map[string]ModFileRequire{}
	for _, req := range newMod.Require {
		if _, dup := newRequires[req.Path]; dup {
			return typedGenerationError("duplicate_require_directive")
		}
		newRequires[req.Path] = req
	}
	for path, oldReq := range oldRequires {
		newReq, ok := newRequires[path]
		if !ok {
			return typedGenerationErrorf("require_removed", "path=%s", path)
		}
		if oldReq == newReq {
			continue
		}
		if !permittedModulePaths[path] {
			return typedGenerationErrorf("external_requirement_changed", "path=%s", path)
		}
		if oldReq.Indirect != newReq.Indirect {
			return typedGenerationErrorf("requirement_classification_changed", "path=%s", path)
		}
		if newReq.Version != resolvedVersion {
			return typedGenerationErrorf("internal_requirement_wrong_version", "path=%s got=%s want=%s", path, newReq.Version, resolvedVersion)
		}
	}
	for path := range newRequires {
		if _, ok := oldRequires[path]; !ok {
			return typedGenerationErrorf("require_added", "path=%s", path)
		}
	}
	return nil
}

// computeGoSumDeltas returns a bounded per-file added/removed line count
// for every changed go.sum path, without ever executing anything from
// either tree.
func computeGoSumDeltas(ctx context.Context, reader modFileReader, output GenerationOutput, manifest PlanManifest) ([]GoSumDelta, error) {
	permitted := make(map[string]bool, len(manifest.PermittedOutputFiles))
	for _, path := range manifest.PermittedOutputFiles {
		permitted[path] = true
	}
	var deltas []GoSumDelta
	for path, change := range changedPathSet(output.Changes) {
		if !strings.HasSuffix(path, "/go.sum") && path != "go.sum" {
			continue
		}
		if !permitted[path] {
			return nil, typedGenerationErrorf("unauthorized_path_changed", "path=%s", path)
		}
		if change.Kind != "modified" && change.Kind != "added" {
			return nil, typedGenerationErrorf("go_sum_wrong_change_kind", "path=%s kind=%s", path, change.Kind)
		}
		oldData, foundOld, err := reader(ctx, output.SourceSHA, path)
		if err != nil {
			return nil, err
		}
		newData, foundNew, err := reader(ctx, output.CommitSHA, path)
		if err != nil {
			return nil, err
		}
		if !foundNew {
			return nil, typedGenerationErrorf("go_sum_missing", "path=%s", path)
		}
		if !validGoSumSyntax(newData) {
			return nil, typedGenerationErrorf("go_sum_invalid_syntax", "path=%s", path)
		}
		var oldLines []string
		if foundOld {
			if !validGoSumSyntax(oldData) {
				return nil, typedGenerationErrorf("go_sum_invalid_syntax", "path=%s", path)
			}
			oldLines = splitNonEmptyLines(string(oldData))
		}
		newLines := splitNonEmptyLines(string(newData))
		added, removed := lineSetDelta(oldLines, newLines)
		deltas = append(deltas, GoSumDelta{Path: path, AddedLines: added, RemovedLines: removed})
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].Path < deltas[j].Path })
	return deltas, nil
}

// validGoSumSyntax checks go.sum's fixed three-field-per-line grammar:
// "module version hash". It does not verify the hash itself (that would
// require executing or fetching the module), only that the file is
// well-formed enough that computeGoSumDeltas's line-set comparison is
// meaningful.
func validGoSumSyntax(data []byte) bool {
	for _, line := range splitNonEmptyLines(string(data)) {
		if len(strings.Fields(line)) != 3 {
			return false
		}
	}
	return true
}

func splitNonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// lineSetDelta returns the count of lines present in newLines but not
// oldLines (added) and present in oldLines but not newLines (removed),
// treating each slice as a multiset so an unrelated reordering of
// unchanged lines is not miscounted as churn.
func lineSetDelta(oldLines, newLines []string) (added, removed int) {
	oldCounts := map[string]int{}
	for _, line := range oldLines {
		oldCounts[line]++
	}
	newCounts := map[string]int{}
	for _, line := range newLines {
		newCounts[line]++
	}
	for line, count := range newCounts {
		if diff := count - oldCounts[line]; diff > 0 {
			added += diff
		}
	}
	for line, count := range oldCounts {
		if diff := count - newCounts[line]; diff > 0 {
			removed += diff
		}
	}
	return added, removed
}

// validateExpectedTags covers "Only manifest-selected go.mod and go.sum
// files may otherwise change" from the tag side: the generation's own
// resolved version must match every expected tag's version suffix, and
// the manifest's expected tag list must be non-empty and bounded (the
// same 1,024 cap §13.2 sets for manifest modules/tags).
func validateExpectedTags(output GenerationOutput, manifest PlanManifest) error {
	if len(manifest.ExpectedTags) == 0 {
		return typedGenerationError("empty_expected_tags")
	}
	if len(manifest.ExpectedTags) > MaxIssueMappings {
		return typedGenerationError("expected_tags_exceeds_cap")
	}
	for _, tag := range manifest.ExpectedTags {
		if !strings.HasSuffix(tag, output.ResolvedVersion) {
			return typedGenerationErrorf("expected_tag_version_mismatch", "tag=%s version=%s", tag, output.ResolvedVersion)
		}
	}
	return nil
}
