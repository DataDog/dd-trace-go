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
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
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

// stdlibPackages are used to skip in version checking.
// TODO: can we make this exported and export from gen_supported_versions_doc.go or put it in instrumentation/packages.go?
var stdLibPackages = map[string]struct{}{
	"log/slog":     {},
	"os":           {},
	"net/http":     {},
	"database/sql": {},
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
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) < 0
	})
	return versions[len(versions)-1]
}

func fetchGoMod(url string) (bool, string) {
	cmd := exec.Command("curl", "-v", "-s", "-f", url)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// If `curl` fails, check if it's because of a 404
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 22 { // HTTP 404
			return false, ""
		}
		fmt.Printf("Error running curl: %v\n", err)
		return false, ""
	}

	// Parse the output to check for a `module` line
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Text()
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

// Comparator for version strings
func compareVersions(v1, v2 string) bool {
	// if v1 > v2 return true
	re := regexp.MustCompile(`^v(\d+)$`)

	// Extract the numeric part from the first version
	match1 := re.FindStringSubmatch(v1)
	match2 := re.FindStringSubmatch(v2)

	if len(match1) < 2 || len(match2) < 2 {
		panic("Invalid version format") // Ensure valid versions like "v1", "v2", etc.
	}

	// Convert the numeric part to integers
	num1, _ := strconv.Atoi(match1[1])
	num2, _ := strconv.Atoi(match2[1])

	if num1 <= num2 {
		return false
	}
	return true
}

func main() {
	log.SetFlags(0) // disable date and time logging
	// Find latest major
	github_latests := map[string]string{}  // map module (base name) => latest on github
	contrib_latests := map[string]string{} // map module (base name) => latest on go.mod

	for pkg, repository := range instrumentation.GetPackages() {

		// Step 1: get the version from the module go.mod
		fmt.Printf("package: %v\n", pkg)
		// fmt.Printf("repository: %v\n", repository)

		// if it is part of the standard packages, continue
		if _, ok := stdLibPackages[repository]; ok {
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

		contrib_latests[base] = version_major_contrib
		// if latestContribMajor, ok := contrib_latests[base]; ok {
		// 	if compareVersions(version_major_contrib, latestContribMajor) {
		// 		contrib_latests[base] = version_major_contrib // TODO: check if this is needed
		// 	}
		// } else {
		// 	contrib_latests[base] = version_major_contrib
		// }

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
		// 5a. If request returns 404, module is not a go module. This means version belongs to the module without any /v at the end.
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
				fmt.Printf("Version %s does not belong to a Go module\n", latest)
				continue
			}
			// latest_major := truncateMajorVersion(latest)
			if latestGithubMajor, ok := github_latests[base]; ok {
				// fmt.Printf("latest github major:%s", latestGithubMajor)
				if compareVersions(major, latestGithubMajor) {
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
			if compareVersions(latestGithubMajor, contribMajor) {
				fmt.Printf("New latest major on Github: %s", latestGithubMajor)
			}
		}
	}

}
