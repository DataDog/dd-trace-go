// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

type ModuleInfo struct {
	Origin struct {
		URL string `json:"url"`
	} `json:"Origin"`
}

type GithubLatests struct {
	Version string
	Module  string
}

type PackageResult struct {
	Base          string
	LatestVersion string
	ModulePath    string
	Error         error
}

func getGoModVersion(repository string, pkg string) (string, string, error) {
	// ex: package: aws/aws-sdk-go-v2
	// repository: github.com/aws/aws-sdk-go-v2
	// look for go.mod in contrib/{package}
	// if it exists, look for repository in the go.mod
	// parse the version associated with repository
	// Define the path to the go.mod file within the contrib/{pkg} directory
	repository = truncateVersionSuffix(repository)

	goModPath := fmt.Sprintf("contrib/%s/go.mod", pkg)

	// Read the go.mod file
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read go.mod file at %s: %w", goModPath, err)
	}

	// Parse the go.mod file
	modFile, err := modfile.Parse(goModPath, content, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse go.mod file at %s: %w", goModPath, err)
	}

	// keep track of largest version from go.mod
	var largestVersion string
	var largestVersionRepo string

	// Search for the module matching the repository
	for _, req := range modFile.Require {
		if strings.HasPrefix(req.Mod.Path, repository) {
			version := req.Mod.Version

			if !semver.IsValid(version) {
				return "", "", fmt.Errorf("invalid semantic version %s in go.mod", version)
			}

			if largestVersion == "" || semver.Compare(version, largestVersion) > 0 {
				largestVersion = version
				largestVersionRepo = req.Mod.Path
			}
		}
	}

	if largestVersion == "" {
		// If the repository is not found in the dependencies
		return "", "", fmt.Errorf("repository %s not found in go.mod file", repository)
	}
	return largestVersionRepo, largestVersion, nil

}

func fetchGoMod(origin, tag string) (*modfile.File, error) {
	// https://raw.githubusercontent.com/gin-gonic/gin/refs/tags/v1.7.7/go.mod
	if !strings.HasPrefix(origin, "https://github.com") {
		return nil, fmt.Errorf("provider not supported: %s", origin)
	}

	repoPath := strings.TrimPrefix(origin, "https://github.com/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/refs/tags/%s/go.mod", repoPath, tag)

	log.Printf("fetching %s\n", url)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return modfile.Parse("go.mod", b, nil)
}

func truncateVersionSuffix(repository string) string {
	parts := strings.Split(repository, "/")
	lastPart := parts[len(parts)-1]

	if len(lastPart) > 1 && strings.HasPrefix(lastPart, "v") && semver.IsValid(lastPart) {
		return strings.Join(parts[:len(parts)-1], "/")
	}

	return repository
}

func getModuleOrigin(repository, version string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", fmt.Sprintf("%s@%s", repository, version))
	cmd.Env = append(os.Environ(), "GOPROXY=direct")
	output, err := cmd.Output()

	if err != nil {
		return "", fmt.Errorf("Failed to execute command: %w", err)
	}

	var moduleInfo ModuleInfo
	if err := json.Unmarshal(output, &moduleInfo); err != nil {
		return "", fmt.Errorf("failed to parse JSON output: %w", err)
	}

	// Check if the Origin.URL field exists
	if moduleInfo.Origin.URL != "" {
		return moduleInfo.Origin.URL, nil
	}

	// If Origin.URL is not found, return an error
	return "", fmt.Errorf("Origin.URL not found in JSON for %s@%s", repository, version)
}

// Run `git ls-remote` and fetch tags
func getTags(vcsURL string) ([]string, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", vcsURL)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	tags := []string{}
	tagRegex := regexp.MustCompile(`refs/tags/(v[\d.]+)$`)
	for scanner.Scan() {
		line := scanner.Text()
		if matches := tagRegex.FindStringSubmatch(line); matches != nil {
			tags = append(tags, matches[1])
		}
	}
	return tags, nil
}

// Group tags by major version
func groupByMajor(tags []string) map[string][]string {
	majors := make(map[string][]string)
	for _, tag := range tags {
		if semver.IsValid(tag) {
			major := semver.Major(tag)
			majors[major] = append(majors[major], tag)
		}
	}
	return majors
}

// Get the latest version for a list of versions
func getLatestVersion(versions []string) string {

	// Filter out pre-release versions and non-valid versions
	validVersions := make([]string, 0)
	for _, v := range versions {
		if semver.IsValid(v) && semver.Prerelease(v) == "" {
			validVersions = append(validVersions, v)
		}
	}
	sort.Slice(validVersions, func(i, j int) bool {
		return semver.Compare(validVersions[i], validVersions[j]) < 0
	})
	return validVersions[len(validVersions)-1]
}

func truncateVersion(pkg string) string {
	// Regular expression to match ".v{X}" or "/v{X}" where {X} is a number
	re := regexp.MustCompile(`(\.v\d+|/v\d+)$`)
	// Replace the matched pattern with an empty string
	return re.ReplaceAllString(pkg, "")
}

// split, ex v5.3.2 => v5
func truncateMajorVersion(version string) string {
	parts := strings.Split(version, ".")
	return parts[0]
}

func main() {
	// log.SetFlags(0) // disable date and time logging
	log.Println("starting")

	// Find latest major
	github_latests := map[string]GithubLatests{} // map module (base name) => latest on github
	contrib_latests := map[string]string{}       // map module (base name) => latest on go.mod

	// var wg sync.WaitGroup
	results := make(chan PackageResult, 10) // Buffered channel to avoid blocking

	for pkg, packageInfo := range instrumentation.GetPackages() {

		// Step 1: get the version from the module go.mod
		fmt.Printf("package: %v\n", pkg)
		repository := packageInfo.TracedPackage

		// if it is part of the standard packages, continue
		if packageInfo.IsStdLib {
			continue
		}

		base := truncateVersion(string(pkg))
		repo, version, err := getGoModVersion(repository, string(pkg))
		if err != nil {
			fmt.Printf("%v go.mod not found.", pkg)
			continue
		}

		// check if need to update contrib_latests
		version_major_contrib := truncateMajorVersion(version)

		if err != nil {
			fmt.Println(err)
			continue
		}

		if current_latest, ok := contrib_latests[base]; ok {
			if semver.Compare(version_major_contrib, current_latest) > 0 {
				contrib_latests[base] = version_major_contrib
			}
		} else {
			contrib_latests[base] = version_major_contrib
		}

		// Step 2:
		// create the string repository@{version} and run command go list -m -json <repository>@<version>
		// this should return a JSON
		// extract Origin[URL] if the JSON contains it, otherwise continue

		origin, err := getModuleOrigin(repo, version)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		} else {
			fmt.Printf("origin URL: %s\n", origin)
		}

		// 2. From the VCS url, do `git ls-remote --tags <vcs_url>` to get the tags
		// output:
		// 3. Parse the tags, and extract all the majors from them (ex v2, v3, v4)
		tags, err := getTags(origin)
		if err != nil {
			fmt.Println("Error fetching tags:", err)
			continue
		}

		majors := groupByMajor(tags)

		// 4. Get the latest version of each major. For each latest version of each major:
		// curl https://raw.githubusercontent.com/<module>/refs/tags/v4.18.3/go.mod
		// 5a. If request returns 404, module is not a go module. This means version belongs to the module without /v at the end.
		// 5b. If request returns a `go.mod`, parse the modfile and extract the mod name

		// Get the latest version for each major
		for _, versions := range majors {
			latest := getLatestVersion(versions)

			log.Printf("fetching go.mod for %s@%s\n", origin, latest)
			f, err := fetchGoMod(origin, latest)
			if err != nil {
				log.Printf("failed to fetch go.mod for %s@%s: %+v\n", origin, latest, err)
				continue
			}
			log.Printf("go.mod for %s@%s: %s\n", origin, latest, f.Module.Mod.Path)
		}
	}

	// Iterate through results
	for result := range results {
		if result.Error != nil {
			fmt.Println("Error:", result.Error)
			continue
		}

		if latestGithub, ok := github_latests[result.Base]; ok {
			if semver.Compare(result.LatestVersion, latestGithub.Version) > 0 {
				github_latests[result.Base] = GithubLatests{result.LatestVersion, result.ModulePath}
			}
		} else {
			github_latests[result.Base] = GithubLatests{result.LatestVersion, result.ModulePath}
		}
	}
	// check if there are any outdated majors
	// output if there is a new major package we do not support
	for base, contribMajor := range contrib_latests {
		if latestGithub, ok := github_latests[base]; ok {
			if semver.Compare(latestGithub.Version, contribMajor) > 0 {
				if base == "go-redis/redis" && latestGithub.Version == "v9" {
					continue // go-redis/redis => redis/go-redis in v9
				}
				fmt.Printf("New latest major %s of repository %s on Github at module: %s\n", latestGithub.Version, base, latestGithub.Module)
				fmt.Printf("latest contrib major: %v\n", contribMajor)
				fmt.Printf("latest github major: %v\n", latestGithub.Version)
			}
		}
	}

}
