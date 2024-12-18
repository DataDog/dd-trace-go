// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"github.com/Masterminds/semver/v3"
	"golang.org/x/mod/modfile"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type Tag struct {
	Name string
}

func getLatestMajorVersion(repo string) (string, error) {
	// Get latest major version available for repo from github.
	const apiURL = "https://api.github.com/repos/%s/tags"
	url := fmt.Sprintf(apiURL, repo)

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch tags: %s", resp.Status)
	}

	var tags []struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", err
	}
	latestByMajor := make(map[int]*semver.Version)

	for _, tag := range tags {
		v, err := semver.NewVersion(tag.Name)
		if err != nil {
			continue // Skip invalid versions
		}

		if v.Prerelease() != "" {
			continue // Ignore pre-release versions
		}

		major := int(v.Major())
		if current, exists := latestByMajor[major]; !exists || v.GreaterThan(current) {
			latestByMajor[major] = v
		}
	}

	var latestMajor *semver.Version
	for _, v := range latestByMajor {
		if latestMajor == nil || v.Major() > latestMajor.Major() {
			latestMajor = v
		}
	}

	if latestMajor != nil {
		return fmt.Sprintf("v%d", latestMajor.Major()), nil
	}

	return "", fmt.Errorf("no valid versions found")

}

func main() {

	data, err := os.ReadFile("integration_go.mod")
	if err != nil {
		fmt.Println("Error reading integration_go.mod:", err)
		return
	}

	modFile, err := modfile.Parse("integration_go.mod", data, nil)
	if err != nil {
		fmt.Println("Error parsing integration_go.mod:", err)
		return
	}

	latestMajor := make(map[string]*semver.Version)

	// Match on versions with /v{major}
	versionRegex := regexp.MustCompile(`^(?P<module>.+?)/v(\d+)$`)

	// Iterate over the required modules and update latest major version if necessary
	for _, req := range modFile.Require {
		module := req.Mod.Path

		if match := versionRegex.FindStringSubmatch(module); match != nil {
			url := match[1]                   //  base module URL (e.g., github.com/foo)
			majorVersionStr := "v" + match[2] // Create semantic version string (e.g., "v2")

			moduleName := strings.TrimPrefix(strings.TrimSpace(url), "github.com/")

			// Parse the semantic version
			majorVersion, err := semver.NewVersion(majorVersionStr)
			if err != nil {
				fmt.Printf("Skip invalid version for module %s: %v\n", module, err)
				continue
			}

			if existing, ok := latestMajor[moduleName]; !ok || majorVersion.GreaterThan(existing) {
				latestMajor[moduleName] = majorVersion
			}
		}
	}

	// Output latest major version that we support.
	// Check if a new major version in Github is available that we don't support.
	// If so, output that a new latest is available.

	// Sort the output
	modules := make([]string, 0, len(latestMajor))
	for module := range latestMajor {
		modules = append(modules, module)
	}
	sort.Strings(modules)

	for _, module := range modules {
		major := latestMajor[module]

		latestVersion, err := getLatestMajorVersion(module) // latest version available
		if err != nil {
			fmt.Printf("Error fetching latest version for module '%s': %v\n", module, err)
			continue
		}

		latestVersionParsed, err := semver.NewVersion(latestVersion)
		if err != nil {
			fmt.Printf("Error parsing latest version '%s' for module '%s': %v\n", latestVersion, module, err)
			continue
		}

		fmt.Printf("Latest DD major version of %s: %d\n", module, major.Major())
		if major.LessThan(latestVersionParsed) {
			fmt.Printf("New latest major version of %s available: %d\n", module, latestVersionParsed.Major())
		}

	}
}
