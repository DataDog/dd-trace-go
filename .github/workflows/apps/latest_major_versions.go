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

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type ModuleInfo struct {
	Origin struct {
		URL string `json:"url"`
	} `json:"Origin"`
}

type GithubLatests struct {
	Version string
	Module  string // the base name of the module
}

func main() {

	// Find latest major
	githubLatests := map[string]GithubLatests{} // map module (base name) => latest on github
	contribLatests := map[string]string{}       // map module (base name) => latest on go.mod

	for pkg, packageInfo := range instrumentation.GetPackages() {

		// 1. Get package and version from the module go.mod
		fmt.Printf("package: %v\n", pkg)
		repository := packageInfo.TracedPackage

		if packageInfo.IsStdLib {
			continue
		}

		base := getBaseVersion(string(pkg))
		repo, version, err := getGoModVersion(repository, string(pkg))
		if err != nil {
			fmt.Printf("%v go.mod not found.", pkg)
			continue
		}

		versionMajorContrib := getMajorVersion(version)

		if currentLatest, ok := contribLatests[base]; ok {
			if semver.Compare(versionMajorContrib, currentLatest) > 0 {
				contribLatests[base] = versionMajorContrib
			}
		} else {
			contribLatests[base] = versionMajorContrib
		}

		// 2. Create the string repository@{version} and run command go list -m -json <repository>@<version>.
		//    This should return a JSON: extract Origin[URL] if the JSON contains it, otherwise continue

		origin, err := getModuleOrigin(repo, version)
		if err != nil {
			log.Printf("failed to get module origin: %s\n", err.Error())
			continue
		}

		// 3. From the VCS url, do `git ls-remote --tags <vcs_url>` to get the tags
		//    Parse the tags, and extract all the majors from them (ex v2, v3, v4)
		tags, err := getTags(origin)
		if err != nil {
			log.Printf("error fetching tags from origin: %s", err.Error())
			continue
		}

		majors := groupByMajor(tags)

		// 4. Get the latest version of each major. For each latest version of each major:
		// curl https://raw.githubusercontent.com/<module>/refs/tags/v4.18.3/go.mod
		// 5a. If request returns 404, module is not a go module. This means version belongs to the module without /v at the end.
		// 5b. If request returns a `go.mod`, parse the modfile and extract the mod name
		// Get the latest version for each major

		for major, versions := range majors {
			latest := getLatestVersion(versions)

			log.Printf("fetching go.mod for %s@%s\n", origin, latest)
			f, err := fetchGoMod(origin, latest)
			if err != nil {
				log.Printf("failed to fetch go.mod for %s@%s: %+v\n", origin, latest, err)
				continue
			}
			if latestGithub, ok := githubLatests[base]; ok {
				if semver.Compare(major, latestGithub.Version) > 0 {
					githubLatests[base] = GithubLatests{major, base}
				}
			} else {
				githubLatests[base] = GithubLatests{major, base}
			}
		}
	}

	// 6. Check if there are any outdated majors
	// 	  Output if there is a new major package we do not support
	for base, contribMajor := range contribLatests {
		if latestGithub, ok := githubLatests[base]; ok {
			if semver.Compare(latestGithub.Version, contribMajor) > 0 {
				if (base == "go-redis/redis" && latestGithub.Version == "v9") || (base == "k8s.io/client-go") {
					// go-redis/redis => redis/go-redis in v9
					// for k8s.io we provide a generic http client middleware that can be plugged with any of the versions
					continue
				}
				fmt.Printf("New latest major %s of repository %s on Github at module: %s\n", latestGithub.Version, base, latestGithub.Module)
				fmt.Printf("latest contrib major: %v\n", contribMajor)
				fmt.Printf("latest github major: %v\n", latestGithub.Version)
			}
		}
	}

}

func fetchGoMod(origin, tag string) (*modfile.File, error) {
	// Parse and process the URL
	repoPath := strings.TrimPrefix(origin, "https://github.com/")
	repoPath = strings.TrimPrefix(repoPath, "https://gopkg.in/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	repoPath = regexp.MustCompile(`\.v\d+$`).ReplaceAllString(repoPath, "")

	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/refs/tags/%s/go.mod", repoPath, tag)

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
	defer resp.Body.Close()

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
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	var moduleInfo ModuleInfo
	if err := json.Unmarshal(output, &moduleInfo); err != nil {
		return "", fmt.Errorf("failed to parse JSON output: %w", err)
	}

	if moduleInfo.Origin.URL == "" {
		// If Origin.URL is not found, return an error
		return "", fmt.Errorf("Origin.URL not found in JSON for %s@%s", repository, version)
	}

	return moduleInfo.Origin.URL, nil
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

func getBaseVersion(pkg string) string {
	// Match on the pattern ".v{X}" or "/v{X}" where {X} is a number
	// Replace the matched pattern with an empty string
	re := regexp.MustCompile(`(\.v\d+|/v\d+)$`)
	return re.ReplaceAllString(pkg, "")
}

func getMajorVersion(version string) string {
	parts := strings.Split(version, ".")
	return parts[0]
}

func getGoModVersion(pkg string, instrumentationName string) (string, string, error) {
	// Look for go.mod in contrib/{package}
	// If go.mod exists, look for repository name in the go.mod
	// Parse the version associated with repository
	// Define the path to the go.mod file within the contrib/{pkg} directory
	pkg = truncateVersionSuffix(pkg)

	goModPath := fmt.Sprintf("contrib/%s/go.mod", instrumentationName)

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

	// Keep track of largest version from go.mod
	var largestVersion string
	var largestVersionRepo string

	// Search for the module matching the repository
	for _, req := range modFile.Require {
		if strings.HasPrefix(req.Mod.Path, pkg) {
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
		// If the repository is not found in the dependencies, return
		return "", "", fmt.Errorf("package %s not found in go.mod file", pkg)
	}
	return largestVersionRepo, largestVersion, nil
}
