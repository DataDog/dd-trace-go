package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
)


func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run get_latest_version.go <owner>/<repo>")
		return
	}

	repo := os.Args[1]
	latestVersion, err := getLatestMajorVersion(repo)
	if err != nil {
		fmt.Printf("Error fetching latest version: %v", err)
		return
	}

	fmt.Printf("Latest major version of %s: %s", repo, latestVersion)
}

func getLatestMajorVersion(repo string) (string, error) {
	// Get latest major version available for repo
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

	// Check latest major version seen
	for _, tag := range tags {
		if majorVersionRegex.MatchString(tag.Name) {
			version := majorVersionRegex.FindString(tag.Name)
			if latest == "" || version > latest {
				latest = version
			}
		}
	}

	return latest, nil
}
