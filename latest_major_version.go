package main

import (
	"bufio"
	"fmt"
	// "strings"
	"os"
	"regexp"
)

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
			module := match[1]
			major := match[2]

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

	fmt.Println("Latest major versions:")
	for module, major := range latestMajor {
		fmt.Printf("Latest version of %s -> v%s\n", module, major)
	}
}
