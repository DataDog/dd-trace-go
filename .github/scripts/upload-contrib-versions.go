package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Payload struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			LanguageLanguage string        `json:"language_language"`
			TracerVersion    string        `json:"tracer_version"`
			Integrations     []Integration `json:"integrations"`
		} `json:"attributes"`
	} `json:"data"`
}

type Integration struct {
	IntegrationName    string `json:"integration_name"`
	IntegrationVersion string `json:"integration_version"`
	DependencyName     string `json:"dependency_name"`
}

func main() {
	filePath := "supported-integration-versions.txt"
	// apiEndpoint := "https://example.com/api"

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	payload := Payload{}
	payload.Data.Type = "supported_integrations"
	payload.Data.ID = "1" // add UTC id here
	payload.Data.Attributes.LanguageLanguage = "go"
	payload.Data.Attributes.TracerVersion = "add-version-here"

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			fmt.Println("Invalid line format:", line)
			continue
		}

		integration := Integration{
			IntegrationName:    parts[0],
			IntegrationVersion: parts[1],
			DependencyName:     parts[0],
		}
		payload.Data.Attributes.Integrations = append(payload.Data.Attributes.Integrations, integration)
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
		return
	}

	// print payload for now
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Println(err)
	}
	fmt.Print(string(b))

	// Convert payload to JSON
	// jsonPayload, err := json.Marshal(payload)
	// if err != nil {
	// 	fmt.Println("Error encoding payload:", err)
	// 	return
	// }

	// // Post JSON payload to API endpoint
	// resp, err := http.Post(apiEndpoint, "application/json", strings.NewReader(string(jsonPayload)))
	// if err != nil {
	// 	fmt.Println("Error posting payload:", err)
	// 	return
	// }
	// defer resp.Body.Close()

	// // Read the response
	// responseBody, err := ioutil.ReadAll(resp.Body)
	// if err != nil {
	// 	fmt.Println("Error reading response body:", err)
	// 	return
	// }

	// fmt.Println("Response:", string(responseBody))
}
