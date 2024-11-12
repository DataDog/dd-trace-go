package main

import (
	"bufio"
	"fmt"
	"strings"
	"os"
	"regexp"
	"encoding/json"
	"net/http"
	// "time"
)

func getLatestMajorVersion(repo string) (string, error) {
	// Get latest major version available for repo from github.
	const apiURL = "https://api.github.com/repos/%s/tags"
	url := fmt.Sprintf(apiURL, repo)

	resp, err := http.Get(url) 	// TODO: figure out ratelimiting??
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch tags: %s\n", resp.Status)
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
	file, err := os.Open("go.mod")
	if err != nil {
		fmt.Println("Error opening go.mod:", err)
		return
	}
	defer file.Close()

	// initialize map: module -> latest major version
	latestMajor := make(map[string]string)

	scanner := bufio.NewScanner(file)

	// Match on versions with /v{major}
	versionRegex := regexp.MustCompile(`^(?P<module>.+?)/v(\d+) v(\d+\.\d+\.\d+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		// Split based on versionRegex to split into module, major versions
		versions := versionRegex.FindStringSubmatch(line)
		// fmt.Println(strings.Join(versions, ","))
		if match := versions; match != nil {
			url := match[1]
			major := match[2]
			module := strings.TrimPrefix(strings.TrimSpace(url), "github.com/")

			// Store the latest major version for the module
			// if module doesn't exist or the new major is greater than existing, update
			if existing, ok := latestMajor[module]; !ok || existing < major {
				latestMajor[module] = major
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading go.mod:", err)
		return
	}

	// Check if a new major version in Github is available that we don't support. If so, output that a new latest is available.
	for module, major := range latestMajor {
		latestVersion, err := getLatestMajorVersion(module)
		if err != nil {
			fmt.Printf("Error fetching latest version: %v", err)
		}

		normalizedMajor := strings.TrimSpace(strings.TrimPrefix(major, "v"))
		normalizedLatestMajor := strings.TrimSpace(strings.TrimPrefix(latestVersion, "v"))
		fmt.Printf("Latest DD version of %s: v%s\n", module, normalizedMajor)

		if normalizedMajor != normalizedLatestMajor {
			fmt.Printf("New latest major version of %s available: v%s\n", module, normalizedLatestMajor)
		}
	}
}
