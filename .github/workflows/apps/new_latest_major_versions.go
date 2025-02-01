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
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"os/exec"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	// Missing dependencies
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
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

func fetchGoModGit(repoURL, tag string, wg *sync.WaitGroup, results chan<- PackageResult) {
	defer wg.Done()

	log.Printf("fetching %s@%s\n", repoURL, tag)
	if !strings.HasSuffix(repoURL, ".git") {
		repoURL = repoURL + ".git"
	}

	storer := memory.NewStorage()
	fs := memfs.New()

	repo, err := git.Clone(storer, fs, &git.CloneOptions{
		URL:           repoURL,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewTagReferenceName(tag),
		Progress:      os.Stdout,
		NoCheckout:    true,
	})
	if err != nil {
		results <- PackageResult{Error: fmt.Errorf("failed to clone repo: %w", err)}
		return
	}

	worktree, err := repo.Worktree()
	if err != nil {
		results <- PackageResult{Error: fmt.Errorf("failed to get worktree: %w", err)}
		return
	}

	err = worktree.Checkout(&git.CheckoutOptions{
		Force:                     true,
		Create:                    false,
		Branch:                    plumbing.NewTagReferenceName(tag),
		SparseCheckoutDirectories: []string{"go.mod"},
	})
	if err != nil {
		results <- PackageResult{Error: fmt.Errorf("failed to checkout file: %w", err)}
		return
	}

	f, err := fs.Open("go.mod")
	if err != nil {
		log.Printf("Failed to open go.mod in repository: %s, tag: %s", repoURL, tag)
		results <- PackageResult{Error: fmt.Errorf("failed to open file: %w", err)}
		return
	}
	defer f.Close()
	// defer func() {
	// 	if cerr := f.Close(); cerr != nil && err == nil {
	// 		err = cerr
	// 	}
	// }()

	b, err := io.ReadAll(f)
	if err != nil {
		results <- PackageResult{Error: fmt.Errorf("failed to read content: %w", err)}
		// return nil, fmt.Errorf("failed to read file content: %w", err)
		return
	}

	mf, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		results <- PackageResult{Error: fmt.Errorf("failed to parse modfile: %w", err)}
		return
	}
	fmt.Printf("Success fetching Go mod")
	results <- PackageResult{ModulePath: mf.Module.Mod.Path, LatestVersion: tag}

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

// func fetchGoMod(url string) (bool, string) {

// 	// Create an HTTP client and make a GET request
// 	resp, err := http.Get(url)
// 	if err != nil {
// 		// fmt.Printf("Error making HTTP request: %v\n", err)
// 		return false, ""
// 		// return false, "", fmt.Errorf("failed to get go.mod from url: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	// Check if the HTTP status is 404
// 	if resp.StatusCode == http.StatusNotFound {
// 		return false, ""
// 	}

// 	// Handle other HTTP errors
// 	if resp.StatusCode != http.StatusOK {
// 		fmt.Printf("Unexpected HTTP status: %d\n", resp.StatusCode)
// 		return false, ""
// 	}

// 	// Read response
// 	reader := bufio.NewReader(resp.Body)
// 	for {
// 		line, err := reader.ReadString('\n')
// 		if err != nil && err != io.EOF {
// 			fmt.Printf("Error reading response body: %v\n", err)
// 			return false, ""
// 		}

// 		if err == io.EOF {
// 			break
// 		}

// 		line = strings.TrimSpace(line)
// 		if strings.HasPrefix(line, "module ") {
// 			return true, strings.TrimSpace(strings.TrimPrefix(line, "module "))
// 		}
// 	}

// 	return false, ""
// }

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
	github_latests := map[string]GithubLatests{} // map module (base name) => latest on github
	contrib_latests := map[string]string{}       // map module (base name) => latest on go.mod

	var wg sync.WaitGroup
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
		fmt.Printf("version: %v\n", version_major_contrib)

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
			// modFile, err := fetchGoModGit(origin, latest)

			// Fetch go.mod in a goroutine
			wg.Add(1)
			go fetchGoModGit(origin, latest, &wg, results)
			// if err != nil {
			// 	continue
			// }

			// fmt.Printf("Latest version for %s: %s\n", major, latest)

			// // Fetch `go.mod`
			// goModURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/refs/tags/%s/go.mod", base, latest)
			// isGoModule, modName := fetchGoMod(goModURL)
			// if isGoModule {
			// 	fmt.Printf("Module name for %s: %s\n", latest, modName)
			// } else {
			// 	continue
			// }
			// latest_major := truncateMajorVersion(latest)
			// if latestGithub, ok := github_latests[base]; ok {
			// 	if semver.Compare(major, latestGithub.Version) > 0 {
			// 		// if latest > latestGithubMajor
			// 		github_latests[base] = GithubLatests{major, modFile.Module.Mod.Path}
			// 	}
			// } else {
			// 	github_latests[base] = GithubLatests{major, modFile.Module.Mod.Path}
			// }

		}

	}

	// Close the results channel once all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

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
