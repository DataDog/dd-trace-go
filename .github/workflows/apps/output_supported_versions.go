// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
)

type ModuleVersion struct {
	Name           string
	MinVersion     string
	MaxVersion     string
	Repository     string
	isInstrumented bool
}

type Integration struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`
}

func isSubdirectory(url, pattern string) bool {
	if strings.HasPrefix(url, pattern) {
		// match is either exact or followed by a "/"
		return len(url) == len(pattern) || url[len(pattern)] == '/'
	}
	return false
}

func fetchLatestVersion(module string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Run `go list -m -versions` to fetch available versions
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-versions", module)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out")
		}
		return "", fmt.Errorf("command error: %s", stderr.String())
	}

	// Parse the output into a list of versions
	versions := strings.Fields(stdout.String())
	if len(versions) == 0 {
		return "", fmt.Errorf("no versions found for module: %s", module)
	}

	// Handle cases with one or more versions
	if len(versions) == 1 {
		// If only one version, fetch the latest version using `go get` and `go list`
		return fetchLatestUsingGoGet(ctx, module)
	}

	// Return the last version (latest) from the versions list
	return versions[len(versions)-1], nil
}

// Helper function to fetch the latest version using `go get` and `go list`
func fetchLatestUsingGoGet(ctx context.Context, module string) (string, error) {
	// Run `go get <module>@latest`
	cmdGet := exec.CommandContext(ctx, "go", "get", module+"@latest")
	if err := cmdGet.Run(); err != nil {
		return "", fmt.Errorf("failed to fetch latest version with go get: %w", err)
	}

	// Run `go list -m <module>` to retrieve the exact version fetched
	cmdList := exec.CommandContext(ctx, "go", "list", "-m", module)
	var stdout, stderr bytes.Buffer
	cmdList.Stdout = &stdout
	cmdList.Stderr = &stderr

	if err := cmdList.Run(); err != nil {
		return "", fmt.Errorf("failed to retrieve module version with go list: %s", stderr.String())
	}

	// Extract the version from the output
	result := strings.Fields(stdout.String())
	if len(result) < 2 {
		return "", fmt.Errorf("unexpected output format from go list: %s", stdout.String())
	}

	return result[1], nil
}

func isModuleInstrumented(moduleName string, instrumentedSet map[string]struct{}) bool {
	// whether the module has automatic tracing supported (by Orchestrion)
	// _, isInstrumented := instrumentedSet[moduleName]
	isInstrumented := false
	for key := range instrumentedSet {
		if isSubdirectory(moduleName, key) {
			isInstrumented = true
			break
		}
	}
	return isInstrumented
}

// parses the go.mod file for a package and extracts the version of a given repository.
func GetMinVersion(packageName, repositoryName string) (ModuleVersion, error) {
	// Path to contrib/{packageName}
	contribPath := filepath.Join("contrib", packageName)
	goModPath := filepath.Join(contribPath, "go.mod")

	// Check if go.mod exists in directory
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		return ModuleVersion{}, fmt.Errorf("go.mod not found in %s", contribPath)
	}

	// Read the go.mod
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("failed to read go.mod: %w", err)
	}

	// Parse the go.mod
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// match the repository name
	repoPattern := fmt.Sprintf(`\b%s\b`, strings.ReplaceAll(repositoryName, "/", `/`))
	repoRegex, err := regexp.Compile(repoPattern)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("invalid repository regex pattern: %w", err)
	}

	// Iterate through require dependencies
	for _, req := range f.Require {
		if repoRegex.MatchString(req.Mod.Path) {
			return ModuleVersion{
				Name:           packageName,
				MinVersion:     req.Mod.Version,
				MaxVersion:     "",
				Repository:     req.Mod.Path,
				isInstrumented: false,
			}, nil
		}
	}

	return ModuleVersion{}, fmt.Errorf("repository %s not found in go.mod", repositoryName)
}

func fetchAllLatestVersions(modules []ModuleVersion) []ModuleVersion {
	// Concurrently fetches the latest version of each module.

	var wg sync.WaitGroup
	var mu sync.Mutex

	updatedModules := make([]ModuleVersion, len(modules))

	wg.Add(len(modules))
	for i, mod := range modules {
		go func(i int, mod ModuleVersion) {
			defer wg.Done()
			latestVersion, err := fetchLatestVersion(mod.Repository)
			if err != nil {
				fmt.Printf("Error fetching latest version for %s: %v\n", mod.Repository, err)
				mu.Lock()
				updatedModules[i] = ModuleVersion{mod.Name, mod.MinVersion, "Error", mod.Repository, mod.isInstrumented}
				mu.Unlock()
				return
			}

			mu.Lock()
			updatedModules[i] = ModuleVersion{
				Name:           mod.Name,
				MinVersion:     mod.MinVersion,
				MaxVersion:     latestVersion,
				Repository:     mod.Repository,
				isInstrumented: mod.isInstrumented,
			}
			mu.Unlock()
		}(i, mod)
	}

	wg.Wait()
	return updatedModules
}

func writeMarkdownFile(modules []ModuleVersion, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer file.Close()

	fmt.Fprintln(file, "| Dependency |    Repository  | Minimum Version | Maximum Version | Auto-Instrumented |")
	fmt.Fprintln(file, "|------------|-----------------|-----------------|-----------------|-----------------|")

	// Sort modules by name
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Name < modules[j].Name
	})

	for _, mod := range modules {
		if mod.Name != "" {
			fmt.Fprintf(file, "| %s | %s | %s | %s | %+v\n", mod.Name, mod.Repository, mod.MinVersion, mod.MaxVersion, mod.isInstrumented)
		}
	}
	return nil
}

func initializeInstrumentedSet() map[string]struct{} {
	// Hardcoded: this is from the table in https://github.com/DataDog/orchestrion
	return map[string]struct{}{
		"database/sql":                                        {},
		"github.com/gin-gonic/gin":                            {},
		"github.com/go-chi/chi/v5":                            {},
		"github.com/go-chi/chi":                               {},
		"github.com/go-redis/redis/v7":                        {},
		"github.com/go-redis/redis/v8":                        {},
		"github.com/gofiber/fiber/v2":                         {},
		"github.com/gomodule/redigo/redis":                    {},
		"github.com/gorilla/mux":                              {},
		"github.com/jinzhu/gorm":                              {},
		"github.com/labstack/echo/v4":                         {},
		"google.golang.org/grpc":                              {},
		"gorm.io/gorm":                                        {},
		"net/http":                                            {},
		"go.mongodb.org/mongo-driver/mongo":                   {},
		"github.com/aws/aws-sdk-go":                           {},
		"github.com/hashicorp/vault":                          {},
		"github.com/IBM/sarama":                               {},
		"github.com/Shopify/sarama":                           {},
		"k8s.io/client-go":                                    {},
		"log/slog":                                            {},
		"os":                                                  {},
		"github.com/aws/aws-sdk-go-v2":                        {},
		"github.com/redis/go-redis/v9":                        {},
		"github.com/gocql/gocql":                              {},
		"cloud.google.com/go/pubsub":                          {},
		"github.com/99designs/gqlgen":                         {},
		"github.com/redis/go-redis":                           {},
		"github.com/graph-gophers/graphql-go":                 {},
		"github.com/graphql-go/graphql":                       {},
		"github.com/jackc/pgx":                                {},
		"github.com/elastic/go-elasticsearch":                 {},
		"github.com/twitchtv/twirp":                           {},
		"github.com/segmentio/kafka-go":                       {},
		"github.com/confluentinc/confluent-kafka-go/kafka":    {},
		"github.com/confluentinc/confluent-kafka-go/kafka/v2": {},
		"github.com/julienschmidt/httprouter":                 {},
		"github.com/sirupsen/logrus":                          {},
	}
}

func processPackages(packageMap map[string]string) ([]ModuleVersion, error) {
	var modules []ModuleVersion
	for pkg, repository := range instrumentation.GetPackages() {
		package_name := string(pkg)
		packageMap[repository] = package_name

		module, err := GetMinVersion(package_name, repository)
		if err != nil {
			continue
		}
		modules = append(modules, module)
	}
	return modules, nil

}

func main() {

	packageMap := make(map[string]string) // map holding package names and repositories
	outputPath := "supported_versions.md"
	instrumentedSet := initializeInstrumentedSet()

	modules, err := processPackages(packageMap)
	if err != nil {
		fmt.Printf("Error processing packages: %v\n", err)
		return
	}

	// update with instrumented status
	for i := range modules {
		modules[i].isInstrumented = isModuleInstrumented(modules[i].Repository, instrumentedSet)
	}

	modulesWithLatest := fetchAllLatestVersions(modules)

	if err := writeMarkdownFile(modulesWithLatest, outputPath); err != nil {
		fmt.Println(err)
	}

	fmt.Println("Version information written to", outputPath)
}
