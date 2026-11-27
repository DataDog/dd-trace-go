// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command gensupportedversions writes supported_versions.json, the list of library
// versions each contrib integration supports. The one-pipeline
// generate-supported-versions job runs it through .gitlab/generate_supported_versions.sh,
// validates the file, and uploads it to the APM Integrations Portal.
//
// Each contrib/**/manifest.yaml gives an integration and the modules it traces. For each
// module, the contrib's go.mod gives the minimum version: Go's minimum version selection
// means an application can't build the contrib against anything older, and the contrib
// tests run against that version. A new major version is a new module path, so ranges
// have no upper bound.
//
// The tested Go runtimes are the current stable and oldstable releases from go.dev, which
// are the versions the GitHub Actions test matrix resolves.
//
// Usage:
//
//	gensupportedversions [-root dir] [-out file]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"
)

const (
	// contribModulePrefix is the module path of every published contrib module. Modules
	// under contrib/ with another path are test fixtures that are never published.
	contribModulePrefix = "github.com/DataDog/dd-trace-go/contrib/"
	goReleasesURL       = "https://go.dev/dl/?mode=json"
	goRuntimeName       = "go"
)

// retryDelay is the delay before the second go.dev request, growing linearly after that.
var retryDelay = 2 * time.Second

// entry is one element of supported_versions.json. The format is shared by all tracers
// and checked by the one-pipeline validate-supported-versions job.
type entry struct {
	LibraryName      string         `json:"libraryName"`
	IntegrationName  string         `json:"integrationName"`
	AutoInstrumented bool           `json:"autoInstrumented"`
	Versions         []versionGroup `json:"versions"`
}

type versionGroup struct {
	TestedRuntimes map[string][]string `json:"testedRuntimes"`
	SupportedRange string              `json:"supportedRange"`
	Tested         []string            `json:"tested"`
}

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

type manifest struct {
	Name     string `yaml:"name"`
	Packages []struct {
		Name string `yaml:"name"`
	} `yaml:"packages"`
}

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "supported_versions.json", "output file")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	goVersions, err := fetchGoVersions(ctx, http.DefaultClient, goReleasesURL)
	if err != nil {
		log.Fatalf("resolving the tested Go versions: %v", err)
	}

	entries, err := generate(*root, goVersions, isStdlib)
	if err != nil {
		log.Fatalf("generating supported versions: %v", err)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		log.Fatalf("encoding supported versions: %v", err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		log.Fatalf("writing %s: %v", *out, err)
	}
	log.Printf("wrote %d entries to %s (Go %s)", len(entries), *out, strings.Join(goVersions, ", "))
}

// generate builds the supported_versions.json entries for the contrib integrations under
// root. goVersions are the tested Go runtimes, and isStdlib reports whether an import path
// belongs to the standard library.
func generate(root string, goVersions []string, isStdlib func(string) bool) ([]entry, error) {
	contribDir := filepath.Join(root, "contrib")
	var manifestDirs []string
	err := filepath.WalkDir(contribDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dir := filepath.Dir(path)
		switch d.Name() {
		case "manifest.yaml":
			manifestDirs = append(manifestDirs, dir)
		case "go.mod":
			return checkHasManifest(contribDir, dir)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var entries []entry
	for _, dir := range manifestDirs {
		es, err := manifestEntries(dir, goVersions, isStdlib)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		entries = append(entries, es...)
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := strings.Compare(a.LibraryName, b.LibraryName); c != 0 {
			return c
		}
		return strings.Compare(a.IntegrationName, b.IntegrationName)
	})
	return entries, nil
}

// checkHasManifest returns an error when dir holds a published contrib module without a
// manifest.yaml, so a new integration can't be left out of the file.
func checkHasManifest(contribDir, dir string) error {
	rel, err := filepath.Rel(contribDir, dir)
	if err != nil {
		return err
	}
	if isHelperDir(rel) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(modfile.ModulePath(data), contribModulePrefix) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.yaml")); err != nil {
		return fmt.Errorf("contrib module %s has no manifest.yaml: %w", dir, err)
	}
	return nil
}

// isHelperDir reports whether dir, relative to contrib/, holds a helper module (otelc
// hooks, test fixtures) rather than an integration.
func isHelperDir(dir string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(dir), "/") {
		if part == "internal" || part == "testdata" || part == "otelc" ||
			strings.HasPrefix(part, "_") || strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func manifestEntries(dir string, goVersions []string, isStdlib func(string) bool) ([]entry, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest.yaml: %w", err)
	}
	if m.Name == "" {
		return nil, errors.New("manifest.yaml has no name")
	}
	auto := fileExists(filepath.Join(dir, "orchestrion.yml")) ||
		fileExists(filepath.Join(dir, "otelc", "otelc.yaml"))
	runtimes := map[string][]string{goRuntimeName: goVersions}

	if len(m.Packages) == 0 {
		// Integrations of a standard library package have no module to read a version
		// from. The others without packages (Azure APIM callout, HAProxy SPOA, Gateway
		// API request mirror) trace traffic from a proxy, not a Go library.
		if !isStdlib(m.Name) {
			return nil, nil
		}
		return []entry{{
			LibraryName:      m.Name,
			IntegrationName:  m.Name,
			AutoInstrumented: auto,
			Versions: []versionGroup{{
				TestedRuntimes: runtimes,
				SupportedRange: "*",
				Tested:         []string{},
			}},
		}}, nil
	}

	modData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod, err := modfile.ParseLax("go.mod", modData, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.mod: %w", err)
	}
	entries := make([]entry, 0, len(m.Packages))
	for _, p := range m.Packages {
		version, ok := requiredVersion(mod, p.Name)
		if !ok {
			return nil, fmt.Errorf("go.mod doesn't require %s, which manifest.yaml lists", p.Name)
		}
		entries = append(entries, entry{
			LibraryName:      p.Name,
			IntegrationName:  m.Name,
			AutoInstrumented: auto,
			Versions: []versionGroup{{
				TestedRuntimes: runtimes,
				// +incompatible is build metadata: it doesn't change the version order,
				// and npm semver ranges reject it.
				SupportedRange: ">=" + strings.TrimSuffix(version, "+incompatible"),
				Tested:         []string{version},
			}},
		})
	}
	return entries, nil
}

func requiredVersion(mod *modfile.File, path string) (string, bool) {
	for _, r := range mod.Require {
		if r.Mod.Path == path {
			return r.Mod.Version, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isStdlib reports whether path is a standard library package of the Go toolchain
// running the generator.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	if strings.Contains(first, ".") {
		return false
	}
	info, err := os.Stat(filepath.Join(build.Default.GOROOT, "src", filepath.FromSlash(path)))
	return err == nil && info.IsDir()
}

// fetchGoVersions returns the stable and oldstable Go releases, oldest first, without the
// "go" prefix (for example "1.26.11" and "1.27.4").
func fetchGoVersions(ctx context.Context, client *http.Client, url string) ([]string, error) {
	var lastErr error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, errors.Join(lastErr, ctx.Err())
			case <-time.After(time.Duration(attempt) * retryDelay):
			}
		}
		versions, err := fetchGoVersionsOnce(ctx, client, url)
		if err == nil {
			return versions, nil
		}
		lastErr = err
		log.Printf("fetching %s (attempt %d): %v", url, attempt+1, err)
	}
	return nil, lastErr
}

func fetchGoVersionsOnce(ctx context.Context, client *http.Client, url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding releases: %w", err)
	}
	return latestStable(releases)
}

// latestStable picks the newest release of the two newest stable Go minor versions.
// go.dev lists releases newest first.
func latestStable(releases []goRelease) ([]string, error) {
	var versions []string
	seenMinor := map[string]bool{}
	for _, r := range releases {
		if !r.Stable {
			continue
		}
		v := strings.TrimPrefix(r.Version, "go")
		major, rest, _ := strings.Cut(v, ".")
		minor, _, _ := strings.Cut(rest, ".")
		key := major + "." + minor
		if seenMinor[key] {
			continue
		}
		seenMinor[key] = true
		versions = append(versions, v)
		if len(versions) == 2 {
			slices.Reverse(versions)
			return versions, nil
		}
	}
	return nil, fmt.Errorf("found %d stable Go minor versions, want 2", len(versions))
}
