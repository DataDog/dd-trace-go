// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// taggerReadOnlyRemote is the fixed --remote argument generate.go passes
// to the tagger. §13.6 requires the generate job to have only read-only
// checkout/download access and no persisted Git credentials; passing a
// name that resolves to nothing real (rather than the caller's actual
// source remote) means a tagger bug that ignored --disable-push and
// attempted a push would fail immediately instead of silently reaching a
// real remote, since generate.go's checkout never configures this
// remote name at all.
const taggerReadOnlyRemote = "gardener-readonly-source"

// runTaggerGeneration invokes the pinned tagger binary against the
// checkout at input.WorkDir twice: once with --plan-json to capture the
// tagger's own declared manifest (its expected tags and permitted output
// files), and once with --format json --disable-push to actually mutate
// the checkout. Both invocations force GOWORK=off, GOPROXY=off, and
// GOSUMDB=off and isolated GOPATH/GOCACHE/GOMODCACHE, so generation can
// only resolve dependencies through verified module-local replace
// directives already present in the source tree, never through a real
// module proxy, registry, or the host's module cache.
func runTaggerGeneration(ctx context.Context, runner CommandRunner, input GenerateInput) error {
	moduleArgs := moduleSelectionArgs(input)
	env := WithCacheDirs(taggerGenerationEnv(), input.CacheDirs)

	planArgs := append([]string{
		"--root", input.WorkDir,
		"--version", input.ResolvedVersion,
		"--plan-json", taggerPlanManifestRelPath,
	}, moduleArgs...)
	if result, err := runner.Run(ctx, Command{Path: input.TaggerBinaryPath, Args: planArgs, Env: env, Dir: input.WorkDir}); err != nil {
		return taggerFailureError(result, err)
	}

	generateArgs := append([]string{
		"--format", "json",
		"--disable-push",
		"--root", input.WorkDir,
		"--version", input.ResolvedVersion,
		"--remote", taggerReadOnlyRemote,
	}, moduleArgs...)
	result, err := runner.Run(ctx, Command{Path: input.TaggerBinaryPath, Args: generateArgs, Env: env, Dir: input.WorkDir})
	if err != nil {
		return taggerFailureError(result, err)
	}
	return nil
}

func moduleSelectionArgs(input GenerateInput) []string {
	var args []string
	if len(input.ExcludedModules) > 0 {
		args = append(args, "--exclude-modules", strings.Join(input.ExcludedModules, ","))
	}
	if len(input.UntaggedModules) > 0 {
		args = append(args, "--untag-modules", strings.Join(input.UntaggedModules, ","))
	}
	if len(input.ExcludedDirs) > 0 {
		args = append(args, "--exclude-dirs", strings.Join(input.ExcludedDirs, ","))
	}
	return args
}

// taggerFailureError turns a failed tagger invocation into a typed
// ReleaseError without exposing raw stderr as a public message (§13.7):
// the tagger's own --format json output on stderr already carries a
// fixed "error" code (see scripts/autoreleasetagger/main.go's
// renderError); that code becomes this error's Detail, never its public
// Code, which stays the fixed "generation_tool_failed".
func taggerFailureError(result CommandResult, cause error) *ReleaseError {
	releaseErr := wrapReleaseError(ErrorClassGenerationFailed, "generation_tool_failed", cause)
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(result.Stderr), &payload) == nil && payload.Error != "" {
		releaseErr.Detail = map[string]string{"tool_error_code": payload.Error}
	}
	return releaseErr
}

// taggerGenerationEnv builds the exact, allowlisted environment for
// invoking the tagger: GOWORK=off (workspace mode is never a substitute
// for verified module-local replace directives, per §14 B07's explicit
// instruction), GOPROXY=off and GOSUMDB=off (no real module proxy or
// checksum database is ever reachable during generation), and isolated
// GOPATH/GOCACHE/GOMODCACHE so a run can never read or pollute the host's
// real module cache. It never inherits the ambient process environment.
func taggerGenerationEnv() []string {
	base := gitStateEnv()
	env := append([]string(nil), base...)
	env = append(env,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOFLAGS=-mod=mod",
		"PATH="+generationPATH(),
	)
	return env
}

// generationPATH builds a minimal, explicit PATH containing only the
// directories that hold the `go` and `git` executables this process
// itself resolved, plus the fixed system directories every supported
// platform ships a shell in. It never copies the ambient process PATH
// wholesale: an inherited PATH could contain a directory writable by an
// unrelated, less-trusted process earlier in the job, letting a same-named
// binary shadow the real `go` or `git`.
func generationPATH() string {
	dirs := []string{"/usr/bin", "/bin"}
	if goPath, err := exec.LookPath("go"); err == nil {
		dirs = append([]string{filepath.Dir(goPath)}, dirs...)
	}
	if gitPath, err := exec.LookPath("git"); err == nil {
		dirs = append([]string{filepath.Dir(gitPath)}, dirs...)
	}
	return strings.Join(dedupeStrings(dirs), string(os.PathListSeparator))
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// TaggerCacheDirs are isolated GOPATH/GOCACHE/GOMODCACHE locations for one
// generation attempt. Callers (tests, and eventually the protected
// generate job) must build these under a fresh, exclusively-owned
// directory and must never point them at a shared or host-level cache.
type TaggerCacheDirs struct {
	GOPATH     string
	GOCACHE    string
	GOMODCACHE string
}

// WithCacheDirs returns env plus GOPATH/GOCACHE/GOMODCACHE set to dirs,
// for callers that need generation to use isolated, fresh caches (every
// test that runs the real tagger binary must call this; see
// generate_test.go).
func WithCacheDirs(env []string, dirs TaggerCacheDirs) []string {
	return append(append([]string(nil), env...),
		"GOPATH="+dirs.GOPATH,
		"GOCACHE="+dirs.GOCACHE,
		"GOMODCACHE="+dirs.GOMODCACHE,
	)
}

// readTaggerManifest reads the tagger's own JSON manifest, produced by
// `--plan-json` at a fixed, well-known relative path inside the
// checkout. generate.go asks the real tagger to also emit its
// --plan-json manifest (see runTaggerGeneration) so validate.go can
// compare the generation's actual result against the tagger's own
// declared intent, rather than trusting only generate.go's independent
// git diff-tree evidence.
func readTaggerManifest(workDir string) (map[string]any, error) {
	path := filepath.Join(workDir, taggerPlanManifestRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassGenerationFailed, "generation_manifest_missing", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, wrapReleaseError(ErrorClassGenerationFailed, "generation_manifest_unparseable", err)
	}
	return manifest, nil
}

// taggerPlanManifestRelPath is the fixed, well-known path generate.go
// asks the tagger to write its --plan-json manifest to, relative to the
// checkout root. It is outside every module's tree (a dot-prefixed name
// at the checkout root) so it can never collide with a real tracked file
// and never becomes part of the generated commit.
const taggerPlanManifestRelPath = ".gardener-release-plan.json"

// PlanManifestFromTaggerOutput converts the tagger's raw --plan-json
// manifest (as decoded generically in GenerationOutput.TaggerManifest)
// into the typed PlanManifest validate.go consumes. It fails closed on
// any missing or wrong-typed field rather than defaulting it, since a
// silently empty PermittedOutputFiles or ExpectedTags would make
// validate.go's allowlist checks vacuously permissive.
func PlanManifestFromTaggerOutput(raw map[string]any) (PlanManifest, error) {
	sourceSHA, ok := raw["source_sha"].(string)
	if !ok || sourceSHA == "" {
		return PlanManifest{}, typedGenerationError("tagger_manifest_missing_source_sha")
	}
	requestedVersion, ok := raw["requested_version"].(string)
	if !ok || requestedVersion == "" {
		return PlanManifest{}, typedGenerationError("tagger_manifest_missing_version")
	}
	rootModulePath, ok := raw["root_module"].(string)
	if !ok || rootModulePath == "" {
		return PlanManifest{}, typedGenerationError("tagger_manifest_missing_root_module")
	}
	modulesRaw, ok := raw["modules"].([]any)
	if !ok || len(modulesRaw) == 0 {
		return PlanManifest{}, typedGenerationError("tagger_manifest_missing_modules")
	}
	modules := make([]PlanManifestModule, 0, len(modulesRaw))
	for _, entryRaw := range modulesRaw {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return PlanManifest{}, typedGenerationError("tagger_manifest_malformed_module")
		}
		path, _ := entry["path"].(string)
		dir, _ := entry["dir"].(string)
		tagged, _ := entry["tagged"].(bool)
		if path == "" {
			return PlanManifest{}, typedGenerationError("tagger_manifest_malformed_module")
		}
		modules = append(modules, PlanManifestModule{Path: path, Dir: dir, Tagged: tagged})
	}
	permitted, err := stringSliceField(raw, "permitted_output_files")
	if err != nil {
		return PlanManifest{}, err
	}
	expectedTags, err := stringSliceField(raw, "expected_tags")
	if err != nil {
		return PlanManifest{}, err
	}
	return PlanManifest{
		SourceSHA:            sourceSHA,
		RequestedVersion:     requestedVersion,
		RootModulePath:       rootModulePath,
		Modules:              modules,
		PermittedOutputFiles: permitted,
		ExpectedTags:         expectedTags,
	}, nil
}

func stringSliceField(raw map[string]any, key string) ([]string, error) {
	valuesRaw, ok := raw[key].([]any)
	if !ok || len(valuesRaw) == 0 {
		return nil, typedGenerationErrorf("tagger_manifest_missing_field", "field=%s", key)
	}
	values := make([]string, 0, len(valuesRaw))
	for _, v := range valuesRaw {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, typedGenerationErrorf("tagger_manifest_malformed_field", "field=%s", key)
		}
		values = append(values, s)
	}
	return values, nil
}
