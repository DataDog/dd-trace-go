// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	sharedTaggerBinaryOnce sync.Once
	sharedTaggerBinaryPath string
	sharedTaggerBinaryErr  error
)

// buildTaggerBinary builds the real scripts/autoreleasetagger module into
// a fresh temp directory using `go build`, exactly as §13.6 requires
// ("Build the standard-library wrapper from the recorded trusted revision
// in a controlled directory... Do not execute a binary from an untrusted
// generation artifact"): this test helper builds from the checked-out
// module tree in this repository, not from any artifact fetched over the
// network. The binary is built once per test process and reused, mirroring
// how a real protected job would build one pinned binary per run rather
// than rebuilding it before every tagger invocation.
func buildTaggerBinary(t *testing.T) string {
	t.Helper()
	sharedTaggerBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gardener-release-tagger-bin-*")
		if err != nil {
			sharedTaggerBinaryErr = err
			return
		}
		binPath := filepath.Join(dir, "autoreleasetagger")
		moduleDir := taggerModuleDir(t)
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = moduleDir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			sharedTaggerBinaryErr = wrapReleaseError(ErrorClassEvidenceIncomplete, "tagger_build_failed", err)
			t.Logf("go build output: %s", out)
			return
		}
		sharedTaggerBinaryPath = binPath
	})
	if sharedTaggerBinaryErr != nil {
		t.Fatalf("build tagger binary: %v", sharedTaggerBinaryErr)
	}
	return sharedTaggerBinaryPath
}

// taggerModuleDir locates scripts/autoreleasetagger relative to this
// package's own directory (scripts/gardener-release), so the helper does
// not depend on the test's working directory or any assumption about
// GOPATH layout.
func taggerModuleDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(wd, "..", "autoreleasetagger")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("locate autoreleasetagger module at %s: %v", dir, err)
	}
	return dir
}

// isolatedCacheDirs returns fresh, exclusively-owned GOPATH/GOCACHE/
// GOMODCACHE directories under t.TempDir(), so a test invocation of the
// real tagger binary can never read or pollute the host's real module
// cache and can never resolve a dependency through anything already
// warmed in a shared cache.
func isolatedCacheDirs(t *testing.T) TaggerCacheDirs {
	t.Helper()
	base := t.TempDir()
	dirs := TaggerCacheDirs{
		GOPATH:     filepath.Join(base, "gopath"),
		GOCACHE:    filepath.Join(base, "gocache"),
		GOMODCACHE: filepath.Join(base, "gomodcache"),
	}
	for _, dir := range []string{dirs.GOPATH, dirs.GOCACHE, dirs.GOMODCACHE} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create isolated cache dir %s: %v", dir, err)
		}
	}
	return dirs
}

// sourceFixtureRepo builds a local bare "source remote" (mirroring
// state_git_test.go's newBareFixtureRemote pattern) seeded with a copy of
// scripts/autoreleasetagger/testdata/root on branch, committed once. It
// returns the bare remote path and the resulting commit SHA. This is
// never a real GitHub remote.
func sourceFixtureRepo(t *testing.T, branch string) (remotePath, sourceSHA string) {
	t.Helper()
	seedDir := filepath.Join(t.TempDir(), "seed")
	if err := copyDir(taggerTestdataRootDir(t), seedDir); err != nil {
		t.Fatalf("copy testdata root: %v", err)
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = seedDir
		cmd.Env = append(gitStateEnv(), "HOME="+seedDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	initCmd := exec.Command("git", "init", "--quiet", "-b", branch, seedDir)
	initCmd.Env = append(gitStateEnv(), "HOME="+seedDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init seed repo: %v\n%s", err, out)
	}
	run("add", "-A")
	run("-c", "user.name=seed", "-c", "user.email=seed@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "seed")
	head := strings.TrimSpace(run("rev-parse", "HEAD"))

	remoteDir := filepath.Join(t.TempDir(), "source-remote.git")
	if err := exec.Command("git", "init", "--quiet", "--bare", remoteDir).Run(); err != nil {
		t.Fatalf("init bare source remote: %v", err)
	}
	run("-c", "protocol.file.allow=always", "push", "--quiet", remoteDir, branch)
	return remoteDir, head
}

type loggingRunner struct {
	Runner CommandRunner
	t      *testing.T
}

func (r *loggingRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	result, err := r.Runner.Run(ctx, command)
	if err != nil {
		r.t.Logf("command failed: %s %v\nstdout=%s\nstderr=%s\nerr=%v", command.Path, command.Args, result.Stdout, result.Stderr, err)
	}
	return result, err
}

// sourceFixtureRepoWithBrokenReplace builds the same fixture as
// sourceFixtureRepo, but deletes moduleA's replace directive for the
// root module before committing. moduleB still requires
// example.com/root/moduleA/v2, and moduleA's go.mod no longer resolves
// example.com/root/v2 through a local replacement, so `go mod tidy` must
// fail (with GOPROXY=off, there is nowhere else to resolve it), modeling
// §15 G06's "required local replacement is missing."
func sourceFixtureRepoWithBrokenReplace(t *testing.T, branch string) (remotePath, sourceSHA string) {
	t.Helper()
	seedDir := filepath.Join(t.TempDir(), "seed-broken")
	if err := copyDir(taggerTestdataRootDir(t), seedDir); err != nil {
		t.Fatalf("copy testdata root: %v", err)
	}
	moduleAGoMod := filepath.Join(seedDir, "moduleA", "go.mod")
	data, err := os.ReadFile(moduleAGoMod)
	if err != nil {
		t.Fatalf("read moduleA go.mod: %v", err)
	}
	broken := strings.Split(string(data), "\n")
	var kept []string
	for _, line := range broken {
		if strings.HasPrefix(strings.TrimSpace(line), "replace") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(moduleAGoMod, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatalf("write broken moduleA go.mod: %v", err)
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = seedDir
		cmd.Env = append(gitStateEnv(), "HOME="+seedDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	initCmd := exec.Command("git", "init", "--quiet", "-b", branch, seedDir)
	initCmd.Env = append(gitStateEnv(), "HOME="+seedDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("init seed repo: %v\n%s", err, out)
	}
	run("add", "-A")
	run("-c", "user.name=seed", "-c", "user.email=seed@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "seed-broken")
	head := strings.TrimSpace(run("rev-parse", "HEAD"))

	remoteDir := filepath.Join(t.TempDir(), "source-remote-broken.git")
	if err := exec.Command("git", "init", "--quiet", "--bare", remoteDir).Run(); err != nil {
		t.Fatalf("init bare source remote: %v", err)
	}
	run("-c", "protocol.file.allow=always", "push", "--quiet", remoteDir, branch)
	return remoteDir, head
}

func taggerTestdataRootDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(wd, "..", "autoreleasetagger", "testdata", "root")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("locate tagger testdata root at %s: %v", dir, err)
	}
	return dir
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// generateWithFixture is a shared helper that builds the tagger binary,
// prepares a source fixture on branch, runs Generate with isolated
// caches and GOPROXY=off, and returns the result and a GitBlobReader
// bound to the same working checkout for validate.go's use. workDir must
// be exclusively owned by the caller (a fresh t.TempDir()). branch is
// both the source fixture's branch and Generate's TargetBranch: every
// B07 test generates in place on the same branch its source commit is
// already on, matching the single-branch/single-version primitive's
// contract (cutting a *new* branch from a *different* source branch is
// two independent Generate calls, out of scope for this helper).
func generateWithFixture(t *testing.T, branch, version string) (GenerationOutput, GitBlobReader) {
	t.Helper()
	remotePath, sourceSHA := sourceFixtureRepo(t, branch)
	workDir := t.TempDir()
	debugRunner := &loggingRunner{Runner: ExecRunner{}, t: t}
	output, err := Generate(context.Background(), debugRunner, GenerateInput{
		SourceRemotePath: remotePath,
		SourceSHA:        sourceSHA,
		TargetBranch:     branch,
		ResolvedVersion:  version,
		UntaggedModules:  []string{"example.com/root/moduleC/v2"},
		TaggerBinaryPath: buildTaggerBinary(t),
		WorkDir:          workDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if err != nil {
		if releaseErr, ok := err.(*ReleaseError); ok {
			t.Fatalf("Generate: %v detail=%v cause=%v", err, releaseErr.Detail, releaseErr.Cause)
		}
		t.Fatalf("Generate: %v", err)
	}
	return output, GitBlobReader{Runner: ExecRunner{}, Dir: workDir}
}
