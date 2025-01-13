package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/semver"
)

func getLatestMajor(repo string) (string, error) {
	// Get the latest major version available on GitHub
	const prefix = "github.com/"
	const apiBaseURL = "https://api.github.com/repos/%s/tags"

	// Truncate "github.com/" and the version suffix
	lastSlashIndex := strings.LastIndex(repo, "/")
	if lastSlashIndex == -1 || !strings.HasPrefix(repo, prefix) {
		return "", fmt.Errorf("invalid repository format: %s", repo)
	}
	trimmedRepo := repo[len(prefix):lastSlashIndex]
	url := fmt.Sprintf(apiBaseURL, trimmedRepo)
	fmt.Printf("Base repo: %s\n", trimmedRepo)

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

// GetLatestMajorContrib finds the latest major version of a repository in the contrib directory.
// TODO: modify this to only walk through contrib once, and store the repository => major versions
func GetLatestMajorContrib(repository string) (string, error) {
	const contribDir = "contrib"

	// Prepare the repository matching pattern -
	// ex. repository = "redis/go-redis" should match on any repository that contains redis/go-redis
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
		// fmt.Printf("Looking for go.mod: %s\n", goModPath)
		if _, err := os.Stat(goModPath); err != nil {
			return nil // No go.mod file, skip
		}

		// Log the path where a go.mod file is found
		fmt.Printf("Found go.mod in: %s\n", goModPath)

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

// // // extractVersionFromGoMod parses the go.mod file and extracts the version of the specified repository.
func extractVersionFromGoMod(goModPath string, repository string) (string, error) {
	file, err := os.Open(goModPath)
	if err != nil {
		return "", fmt.Errorf("failed to open go.mod: %v", err)
	}
	defer file.Close()

	repoPattern := fmt.Sprintf(`\b%s\b`, strings.ReplaceAll(repository, "/", "/"))
	repoRegex, err := regexp.Compile(repoPattern)
	if err != nil {
		return "", fmt.Errorf("invalid repository regex pattern: %v", err)
	}

	var version string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Match lines in the require block with the repository pattern
		if repoRegex.MatchString(line) {
			parts := strings.Fields(line)
			if len(parts) >= 2 && semver.IsValid(parts[1]) {
				version = parts[1]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading go.mod: %v", err)
	}

	if version == "" {
		return "", errors.New("no valid version found for repository in go.mod")
	}

	return version, nil
}

// ValidateRepository checks if the repository string starts with "github.com" and ends with a version suffix.
// Returns true if the repository is valid, false otherwise.
func ValidateRepository(repo string) bool {
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

	// packageMap := make(map[string]string) // map holding package names and repositories
	// var modules []ModuleVersion // this should store the module/contrib name, and the version
	packages := instrumentation.GetPackages()
	integrations := make(map[string]struct{})

	for pkg, info := range packages {
		fmt.Printf("Package: %s, Traced Package: %s\n", pkg, info.TracedPackage)
		repository := info.TracedPackage

		// repo starts with github and ends in version suffix
		// Get the latest major on GH
		if ValidateRepository(repository) {
			latest_major_version, err := getLatestMajor(repository)
			if err != nil {
				fmt.Printf("Error getting min version for repo %s: %v\n", repository, err)
				continue
			}
			const prefix = "github.com"

			// if we've seen this baseRepo before, continue

			lastSlashIndex := strings.LastIndex(repository, "/")
			baseRepo := repository[len(prefix)+1 : lastSlashIndex] // TODO: tidy this
			if _, exists := integrations[baseRepo]; exists {       // check for duplicates
				continue
			}

			integrations[baseRepo] = struct{}{}

			latest_major_contrib, err := GetLatestMajorContrib(baseRepo)
			if err != nil {
				fmt.Printf("Error getting latest major from go.mod for %s: %v\n", repository, err)
			}

			if semver.Major(latest_major_contrib) != latest_major_version {
				fmt.Printf("major on go.mod: %s\n", semver.Major(latest_major_contrib))
				fmt.Printf("major on github: %s\n", latest_major_version)
				fmt.Printf("repository %s has a new major latest on github: %s\n", baseRepo, latest_major_version)

			}

		}
	}
}
