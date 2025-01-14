package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func getLatestMajor(repo string) (string, error) {
	// Get the latest major version available on GitHub
	const prefix = "github.com/"
	const apiBaseURL = "https://api.github.com/repos/%s/tags"
	url := fmt.Sprintf(apiBaseURL, repo)

	// Fetch tags from GitHub
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch tags: %s", resp.Status)
	}

	// Parse response
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", err
	}

	// Track latest versions by major version
	latestByMajor := make(map[string]string) // Map major version to the latest full version string
	for _, tag := range tags {
		v := semver.Canonical(tag.Name) // Ensure the version is canonical
		if !semver.IsValid(v) {
			continue // Skip invalid versions
		}

		if semver.Prerelease(v) != "" {
			continue // Skip pre-release versions
		}

		major := semver.Major(v) // Extract the major version (e.g., "v1")
		if current, exists := latestByMajor[major]; !exists || semver.Compare(v, current) > 0 {
			latestByMajor[major] = v
		}
	}

	// Determine the largest major version
	var latestMajor string
	for major, _ := range latestByMajor {
		if latestMajor == "" || semver.Compare(major, latestMajor) > 0 {
			latestMajor = major
		}
	}

	if latestMajor != "" {
		return latestMajor, nil
	}

	return "", fmt.Errorf("no valid versions found")
}

// 	// walk through contrib directory
// 	// match on anything that matches the pattern "contrib/{repository}" (there may be multiple)
// 	// ex. if repository=go-redis/redis, "contrib/go-redis/redis.v9"
// 	// inside this repository, look for go.mod
// 	// if no go.mod, error
// 	// match on repository in the go.mod in the require() block that contains repository
// 	// example: if repository=go-redis/redis, should match on line "github.com/redis/go-redis/v9 v9.1.0"
// 	// if no repository exists that matches this pattern, error
// 	// return the largest major version associated

// getLatestMajorContrib finds the latest major version of a repository in the contrib directory.
func getLatestMajorContrib(repository string) (string, error) {
	const contribDir = "contrib"

	// Prepare the repository matching pattern
	// ex. repository = "redis/go-redis" should match on any directory that contains redis/go-redis
	repoPattern := fmt.Sprintf(`^%s/%s(/.*)?`, contribDir, strings.ReplaceAll(repository, "/", "/"))

	// Compile regex for matching contrib directory entries
	repoRegex, err := regexp.Compile(repoPattern)
	if err != nil {
		return "", fmt.Errorf("invalid repository pattern: %v", err)
	}

	var latestVersion string

	// Walk through the contrib directory
	err = filepath.Walk(contribDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check if the path matches the contrib repository pattern
		if !info.IsDir() || !repoRegex.MatchString(path) {
			return nil
		}

		// Look for go.mod file
		goModPath := filepath.Join(path, "go.mod")
		if _, err := os.Stat(goModPath); err != nil {
			return nil // No go.mod file, skip
		}

		// Parse the go.mod file to find the version
		version, err := extractVersionFromGoMod(goModPath, repository)
		if err != nil {
			return nil
		}

		// Update the latest version if it's greater
		if latestVersion == "" || semver.Compare(version, latestVersion) > 0 {
			latestVersion = version
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error walking contrib directory: %v", err)
	}

	if latestVersion == "" {
		return "", errors.New("no matching contrib repository with a valid version found")
	}

	return latestVersion, nil
}

func extractVersionFromGoMod(goModPath string, repository string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod: %v", err)
	}
	var versions []string

	// parse go.mod
	modFile, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", fmt.Errorf("failed to parse go.mod: %v", err)
	}

	for _, req := range modFile.Require {
		if strings.Contains(req.Mod.Path, repository) { // check if module path contains repository substring
			if semver.IsValid(req.Mod.Version) {
				versions = append(versions, req.Mod.Version)
			}
		}

	}

	if len(versions) == 0 {
		return "", errors.New("no valid versions found for repository in go.mod")
	}

	// Sort versions in descending order
	// Return the greatest version
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) > 0
	})

	return versions[0], nil

}

// validateRepository checks if the repository string starts with "github.com" and ends with a version suffix.
// Returns true if the repository is valid, false otherwise.
func validateRepository(repo string) bool {
	const prefix = "github.com/"
	if !strings.HasPrefix(repo, prefix) {
		return false
	}

	lastSlashIndex := strings.LastIndex(repo, "/")
	if lastSlashIndex == -1 || !strings.HasPrefix(repo[lastSlashIndex:], "/v") {
		return false
	}

	return true
}

func main() {
	log.SetFlags(0) // disable date and time logging
	packages := instrumentation.GetPackages()
	integrations := make(map[string]struct{})

	for _, info := range packages {
		repository := info.TracedPackage

		// repo starts with github and ends in version suffix
		if validateRepository(repository) {
			const prefix = "github.com"

			// check if we've seen this integration before
			lastSlashIndex := strings.LastIndex(repository, "/")
			baseRepo := repository[len(prefix)+1 : lastSlashIndex]
			if _, exists := integrations[baseRepo]; exists {
				continue
			}

			log.Printf("Base repo: %s\n", baseRepo)

			// Get the latest major from GH
			latest_major_version, err := getLatestMajor(baseRepo)
			if err != nil {
				log.Printf("Error getting min version for repo %s: %v\n", repository, err)
				continue
			}
			integrations[baseRepo] = struct{}{}

			// Get the latest major from go.mod
			latest_major_contrib, err := getLatestMajorContrib(baseRepo)

			if err != nil {
				log.Printf("Error getting latest major from go.mod for %s: %v\n", repository, err)
			}
			log.Printf("major on go.mod: %s\n", semver.Major(latest_major_contrib))
			log.Printf("major on github: %s\n", latest_major_version)

			if semver.Major(latest_major_contrib) != latest_major_version {
				log.Printf("repository %s has a new major latest on github: %s\n", baseRepo, latest_major_version)
			}

		}
	}

}
