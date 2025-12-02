// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"bytes"
	_ "embed" // For go:embed
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/knownmetrics"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// This represents the base64-encoded URL of api.github.com to download the configuration file.
// This can be easily decoded manually, but it is encoded to prevent the URL from being scanned by bots.
const (
	commonMetricsURL = "aHR0cHM6Ly9hcGkuZ2l0aHViLmNvbS9yZXBvcy9EYXRhRG9nL2RkLWdvL2NvbnRlbnRzL3RyYWNlL2FwcHMvdHJhY2VyLXRlbGVtZXRyeS1pbnRha2UvdGVsZW1ldHJ5LW1ldHJpY3Mvc3RhdGljL2NvbW1vbl9tZXRyaWNzLmpzb24="
	goMetricsURL     = "aHR0cHM6Ly9hcGkuZ2l0aHViLmNvbS9yZXBvcy9EYXRhRG9nL2RkLWdvL2NvbnRlbnRzL3RyYWNlL2FwcHMvdHJhY2VyLXRlbGVtZXRyeS1pbnRha2UvdGVsZW1ldHJ5LW1ldHJpY3Mvc3RhdGljL2dvbGFuZ19tZXRyaWNzLmpzb24="
)

//go:embed template.tmpl
var codegenTemplate string

func base64Decode(encoded string) string {
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	return string(decoded)
}

func downloadFromDdgo(remoteURL, localPath, branch, token string, getMetricNames func(map[string]any) []knownmetrics.Declaration, symbolName string) error {
	request, err := http.NewRequest(http.MethodGet, remoteURL+"?ref="+url.QueryEscape(branch), nil)
	if err != nil {
		return err
	}

	// Following the documentation described here:
	// https://docs.github.com/en/rest/repos/contents?apiVersion=2022-11-28

	request.Header.Add("Authorization", "Bearer "+token)
	request.Header.Add("Accept", "application/vnd.github.v3.raw")
	request.Header.Add("X-GitHub-Api-Version", "2022-11-28")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %s", response.Status)
	}

	var decoded map[string]any

	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return err
	}

	metricNames := getMetricNames(decoded)
	slices.SortStableFunc(metricNames, func(i, j knownmetrics.Declaration) int {
		if i.Namespace != j.Namespace {
			return strings.Compare(string(i.Namespace), string(j.Namespace))
		}
		if i.Type != j.Type {
			return strings.Compare(string(i.Type), string(j.Type))
		}
		return strings.Compare(i.Name, j.Name)
	})

	fp, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer fp.Close()

	codegen := template.Must(template.New("").Parse(codegenTemplate))
	return codegen.Execute(fp, map[string]any{
		"symbolName": symbolName,
		"metrics":    metricNames,
	})
}

func getCommonMetricNames(input map[string]any) []knownmetrics.Declaration {
	var names []knownmetrics.Declaration
	for category, value := range input {
		if strings.HasPrefix(category, "$") {
			continue
		}

		metrics := value.(map[string]any)
		for metricKey, value := range metrics {
			metric := knownmetrics.Declaration{
				Namespace: transport.Namespace(category),
				Name:      metricKey,
				Type:      transport.MetricType(value.(map[string]any)["metric_type"].(string)),
			}
			names = append(names, metric)
			if aliases, ok := value.(map[string]any)["aliases"]; ok {
				for _, alias := range aliases.([]any) {
					metric.Name = alias.(string)
					names = append(names, metric)
				}
			}
		}
	}
	return names
}

func getGoMetricNames(input map[string]any) []knownmetrics.Declaration {
	var names []knownmetrics.Declaration
	for key, value := range input {
		if strings.HasPrefix(key, "$") {
			continue
		}
		names = append(names, knownmetrics.Declaration{
			Name: key,
			Type: transport.MetricType(value.(map[string]any)["metric_type"].(string)),
		})
	}
	return names
}

func main() {
	branch := flag.String("branch", "prod", "The branch to get the configuration from")
	flag.Parse()

	githubToken := env.Get("GITHUB_TOKEN")
	if githubToken == "" {
		if _, err := exec.LookPath("gh"); err != nil {
			fmt.Println("Please specify a GITHUB_TOKEN environment variable or install the GitHub CLI.")
			os.Exit(2)
		}

		var buf bytes.Buffer
		cmd := exec.Command("gh", "auth", "token")
		cmd.Stdout = &buf
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Println("Failed to run `gh auth token`:", err)
			os.Exit(1)
		}

		githubToken = strings.TrimSpace(buf.String())
	}

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	if err := downloadFromDdgo(base64Decode(commonMetricsURL), filepath.Join(dir, "..", "known_metrics.common.go"), *branch, githubToken, getCommonMetricNames, "commonMetrics"); err != nil {
		fmt.Println("Failed to download common metrics:", err)
		os.Exit(1)
	}

	if err := downloadFromDdgo(base64Decode(goMetricsURL), filepath.Join(dir, "..", "known_metric.golang.go"), *branch, githubToken, getGoMetricNames, "golangMetrics"); err != nil {
		fmt.Println("Failed to download golang metrics:", err)
		os.Exit(1)
	}
}
