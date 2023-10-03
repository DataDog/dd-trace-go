package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Payload struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			TracerLanguage string        `json:"tracer_language"`
			TracerVersion  string        `json:"tracer_version"`
			Integrations   []Integration `json:"integrations"`
		} `json:"attributes"`
	} `json:"data"`
}

type Integration struct {
	Name           string `json:"integration_name"`
	Version        string `json:"integration_version"`
	DependencyName string `json:"dependency_name"`
}

const (
	apiEndpoint = "https://example.com/api"
)

func main() {

	payload := Payload{}
	payload.Data.Type = "supported_integrations"
	payload.Data.ID = "1" // add UTC id here
	payload.Data.Attributes.TracerLanguage = "golang"
	payload.Data.Attributes.TracerVersion = "add-version-here"

	// Function to get the inputted integration's name
	processIntegration := func(integration string) {
		dependency := ""
		// Run go list command to get the version
		cmd := exec.Command("go", "list", "-mod=mod", "-m", "-f", "{{ .Version }}", integration)
		output, err := cmd.Output()
		if err != nil {

			// Try to remove the version from the dependency name since no version was previously found
			re := regexp.MustCompile(`\.v\d+`)
			dependency = re.ReplaceAllString(integration, "")

			cmd = exec.Command("go", "list", "-mod=mod", "-m", "-f", "{{ .Version }}", dependency)
			output, err = cmd.Output()
			if err != nil {
				// Check for version of the directory name, ie: k8s.io/client-go/kubernetes" -> k8s.io/client-go as a final effort
				dependency = filepath.Dir(integration)
				cmd = exec.Command("go", "list", "-mod=mod", "-m", "-f", "{{ .Version }}", dependency)
				output, err = cmd.Output()
				if err != nil {
					fmt.Println("FAILURE: No match found for", integration)
					return
				}
			}
		}

		// add version and integration to the payload
		version := strings.TrimSpace(string(output))
		dependencyName := integration
		if dependency != "" {
			dependencyName = dependency
		}
		integrationPayload := Integration{
			Name:           integration,
			Version:        version,
			DependencyName: dependencyName,
		}
		payload.Data.Attributes.Integrations = append(payload.Data.Attributes.Integrations, integrationPayload)
	}

	// Loop through subdirectories of "contrib"
	subdirs, err := filepath.Glob("contrib/*")
	if err != nil {
		fmt.Println("Failed to read subdirectories:", err)
		return
	}

	for _, subdir := range subdirs {
		// Check if subdir is a directory
		info, err := os.Stat(subdir)
		if err != nil || !info.IsDir() {
			continue
		}

		// Parse file to get the integration name from tracer.MarkIntegrationImported function
		cmd := exec.Command("grep", "-r", "-Eo", "tracer\\.MarkIntegrationImported\\([^)]+\\)", subdir)
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			line := scanner.Text()

			// Extract the variable name from the line
			variable := "\"" + strings.TrimPrefix(strings.TrimSuffix(line[strings.Index(line, "(")+1:], ")"), "\"")

			// Check if the variable has quotes on both sides, in which case it is the dependency name
			if strings.HasPrefix(variable, "\"") && strings.HasSuffix(variable, "\"") {
				variable = strings.Trim(variable, "\"")

				// parse the version
				processIntegration(variable)
			} else {
				// otherwise parse the variable and get the variable value from the file
				file := strings.Split(line, ":")[0]

				variable = strings.Trim(variable, "\"")

				// Find the value of the variable in the file
				cmd := exec.Command("grep", "-E", variable+"\\s*=", file)
				output, err := cmd.Output()
				if err != nil {
					fmt.Println("No match found for", file)
					continue
				}

				value := strings.Split(strings.TrimSpace(string(output)), "\"")[1]

				// try to get the version if we found a value
				if value != "" {
					// parse the version
					processIntegration(value)
				} else {
					fmt.Println("No match found for", file)
				}
			}
		}
	}

	// Define a flag for dry run
	dryRun := flag.Bool("dry-run", false, "Print the output instead of making the HTTP request")
	flag.Parse()

	if *dryRun {
		// Print payload
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			log.Fatalf("Error print the payload: %s", err)
		}
		fmt.Print(string(b))
	} else {
		// Convert payload to JSON
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			log.Fatalf("Error encoding the payload: %s", err)
		}

		// Post JSON payload to API endpoint
		resp, err := http.Post(apiEndpoint, "application/json", strings.NewReader(string(jsonPayload)))
		if err != nil {
			log.Fatalf("Error posting the payload: %s", err)
		}
		defer resp.Body.Close()

		// Read the response
		responseBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Error reading the response body: %s", err)
		}

		fmt.Println("Response:", string(responseBody))
	}
}
