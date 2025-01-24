// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"os/exec"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

type ModuleInfo struct {
	Origin struct {
		URL string `json:"url"`
	} `json:"Origin"`
}

func getGoModVersion(repository string, pkg string) (string, error) {
	// ex: package: aws/aws-sdk-go-v2
	// repository: github.com/aws/aws-sdk-go-v2
	// look for go.mod in contrib/{package}
	// if it exists, look for repository in the go.mod
	// parse the version associated with repository
	// Define the path to the go.mod file within the contrib/{pkg} directory
	goModPath := fmt.Sprintf("contrib/%s/go.mod", pkg)

	// Read the go.mod file
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod file at %s: %w", goModPath, err)
	}

	// Parse the go.mod file
	modFile, err := modfile.Parse(goModPath, content, nil)
	if err != nil {
		return "", fmt.Errorf("failed to parse go.mod file at %s: %w", goModPath, err)
	}

	// Search for the module matching the repository
	for _, req := range modFile.Require {
		if strings.HasPrefix(req.Mod.Path, repository) {
			return req.Mod.Version, nil
		}
	}

	// If the repository is not found in the dependencies
	return "", fmt.Errorf("repository %s not found in go.mod file", repository)
}

func getModuleOrigin(repository, version string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", fmt.Sprintf("%s@%s", repository, version))
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

func fetchGoMod(url string) (bool, string) {

	// Create an HTTP client and make a GET request
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error making HTTP request: %v\n", err)
		return false, ""
	}
	defer resp.Body.Close()

	// Check if the HTTP status is 404
	if resp.StatusCode == http.StatusNotFound {
		return false, ""
	}

	// Handle other HTTP errors
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Unexpected HTTP status: %d\n", resp.StatusCode)
		return false, ""
	}

	// Read response
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Printf("Error reading response body: %v\n", err)
			return false, ""
		}

		if err == io.EOF {
			break
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return true, strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}

	return false, ""
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
	log.SetFlags(0) // disable date and time logging
	// Find latest major
	github_latests := map[string]string{}  // map module (base name) => latest on github
	contrib_latests := map[string]string{} // map module (base name) => latest on go.mod

	for pkg, repository := range instrumentation.GetPackages() {

		// Step 1: get the version from the module go.mod
		fmt.Printf("package: %v\n", pkg)

		// if it is part of the standard packages, continue
		if _, ok := instrumentation.StandardPackages[repository]; ok {
			continue
		}

		base := truncateVersion(string(pkg))
		version, err := getGoModVersion(repository, string(pkg))
		if err != nil {
			fmt.Printf("%v go.mod not found.", pkg)
			continue
		}
		fmt.Printf("version: %v\n", version)

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

		origin, err := getModuleOrigin(repository, version)
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
		for major, versions := range majors {
			latest := getLatestVersion(versions)
			fmt.Printf("Latest version for %s: %s\n", major, latest)

			// Fetch `go.mod` with command
			// curl https://raw.githubusercontent.com/<module>/refs/tags/<latest>/go.mod
			goModURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/refs/tags/%s/go.mod", base, latest)
			isGoModule, modName := fetchGoMod(goModURL)
			if isGoModule {
				fmt.Printf("Module name for %s: %s\n", latest, modName)
			} else {
				// fmt.Printf("Version %s does not belong to a Go module\n", latest)
				continue
			}
			// latest_major := truncateMajorVersion(latest)
			if latestGithubMajor, ok := github_latests[base]; ok {
				if semver.Compare(major, latestGithubMajor) > 0 {
					// if latest > latestGithubMajor
					github_latests[base] = major
				}
			} else {
				github_latests[base] = major
			}
		}

	}

	// check if there are any outdated majors
	// output if there is a new major package we do not support
	for base, contribMajor := range contrib_latests {
		if latestGithubMajor, ok := github_latests[base]; ok {
			if semver.Compare(latestGithubMajor, contribMajor) > 0 {
				fmt.Printf("New latest major on Github: %s", latestGithubMajor)
			}
		}
	}

}
