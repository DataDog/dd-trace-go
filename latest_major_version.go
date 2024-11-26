// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/mod/modfile"
	"net/http"
	"os"
	"regexp"
	"strings"
)

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

	latest := ""
	// match on version strings e.x. v9.3.0
	majorVersionRegex := regexp.MustCompile(`^v(\d+)`)
	alphaBetaRegex := regexp.MustCompile(`alpha|beta`)

	// Check latest major version seen
	for _, tag := range tags {
		if majorVersionRegex.MatchString(tag.Name) && !alphaBetaRegex.MatchString(tag.Name) {
			version := majorVersionRegex.FindString(tag.Name)
			if latest == "" || version > latest {
				latest = version
			}
		}
	}

	return latest, nil
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

	latestMajor := make(map[string]string)

	// Match on versions with /v{major}
	versionRegex := regexp.MustCompile(`^(?P<module>.+?)/v(\d+)$`)

	// Iterate over the required modules and update latest major version if necessary
	for _, req := range modFile.Require {
		module := req.Mod.Path

		if match := versionRegex.FindStringSubmatch(module); match != nil {
			url := match[1]   //  base module URL (e.g., github.com/foo)
			major := match[2] //  major version (e.g., 2)

			moduleName := strings.TrimPrefix(strings.TrimSpace(url), "github.com/")

			if existing, ok := latestMajor[moduleName]; !ok || existing < major {
				latestMajor[moduleName] = major
			}
		}
	}

	// Output latest major version that we support.
	// Check if a new major version in Github is available that we don't support.
	// If so, output that a new latest is available.
	for module, major := range latestMajor {
		latestVersion, err := getLatestMajorVersion(module)
		if err != nil {
			fmt.Printf("Error fetching latest version for module '%s': %v\n", module, err)
			continue
		}

		normalizedMajor := strings.TrimSpace(strings.TrimPrefix(major, "v"))
		normalizedLatestMajor := strings.TrimSpace(strings.TrimPrefix(latestVersion, "v"))
		fmt.Printf("Latest DD major version of %s: v%s\n", module, normalizedMajor)

		if normalizedMajor != normalizedLatestMajor {
			fmt.Printf("New latest major version of %s available: v%s\n", module, normalizedLatestMajor)
		}
	}
}
