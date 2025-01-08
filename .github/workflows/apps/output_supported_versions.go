// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"regexp"
	"strings"
	"sync"
	"time"
	"path/filepath"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type ModuleVersion struct {
	Name        string
	MinVersion  string
	MaxVersion  string
	Repository  string
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
	// Fetches latest version with `go list`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-versions", module)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("command timed out")
	}
	if err != nil {
		return "", fmt.Errorf("command error: %s", stderr.String())
	}

	versions := strings.Fields(stdout.String())
	if len(versions) < 1 {
		return "", fmt.Errorf("no versions found for module: %s", module)
	}

	return versions[len(versions)-1], nil
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

	// Open and parse the go.mod file
	file, err := os.Open(goModPath)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("failed to open go.mod: %w", err)
	}
	defer file.Close()

	requireRegex := regexp.MustCompile(`^\s*([^\s]+)\s+v([0-9]+\.[0-9]+\.[0-9]+.*)`)

	scanner := bufio.NewScanner(file)
	inRequireBlock := false

	var minVersion string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "require (") {
			inRequireBlock = true
			continue
		}

		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}

		// Check for inline require statement (not in a block)
		if !inRequireBlock && strings.HasPrefix(line, "require ") {
			line = strings.TrimPrefix(line, "require ")
		}

		if inRequireBlock || strings.HasPrefix(line, repositoryName) {  // Process lines inside the require block or single-line requires
			match := requireRegex.FindStringSubmatch(line)
			if match != nil && match[1] == repositoryName {
				minVersion = match[2]
				break // Stop once we find the desired repository
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return ModuleVersion{}, fmt.Errorf("error reading go.mod: %w", err)
	}

	if minVersion == "" {
		return ModuleVersion{}, fmt.Errorf("repository %s not found in go.mod", repositoryName)
	}

	// Return the module info
	return ModuleVersion{
		Name: packageName,
		Repository:  repositoryName,
		MinVersion:  minVersion,
	}, nil
}

func fetchAllLatestVersions(modules []ModuleVersion) []ModuleVersion {
	// Concurrently fetches the latest version of each module.
		
	var wg sync.WaitGroup
	var mu sync.Mutex

	updatedModules := make([]ModuleVersion, 0, len(modules))

	wg.Add(len(modules))
	for _, mod := range modules {
		go func(mod ModuleVersion) {
			defer wg.Done()
			latestVersion, err := fetchLatestVersion(mod.Repository)
			if err != nil {
				fmt.Printf("Error fetching latest version for %s: %v\n", mod.Repository, err)
				mu.Lock()
				updatedModules = append(updatedModules, ModuleVersion{mod.Name, mod.MinVersion, "Error", mod.Repository, mod.isInstrumented})
				mu.Unlock()
				return
			}

			mu.Lock()
			updatedModules = append(updatedModules, ModuleVersion{mod.Name, 
				mod.MinVersion, latestVersion, mod.Repository, mod.isInstrumented})
			mu.Unlock()
		}(mod)
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
			fmt.Fprintf(file, "| %s | %s | v%s | %s | %+v\n", mod.Name, mod.Repository, mod.MinVersion, mod.MaxVersion, mod.isInstrumented)
	}	
}
	return nil
}

func initializeInstrumentedSet() map[string]struct{} {
	// Hardcoded: this is from the table in https://github.com/DataDog/orchestrion
	return map[string]struct{}{
		"database/sql": {},
		"github.com/gin-gonic/gin": {},
		"github.com/go-chi/chi/v5": {},
		"github.com/go-chi/chi": {},
		"github.com/go-redis/redis/v7": {},
		"github.com/go-redis/redis/v8": {},
		"github.com/gofiber/fiber/v2": {},
		"github.com/gomodule/redigo/redis": {},
		"github.com/gorilla/mux": {},
		"github.com/jinzhu/gorm": {},
		"github.com/labstack/echo/v4": {},
		"google.golang.org/grpc": {},
		"gorm.io/gorm": {},
		"net/http": {},
		"go.mongodb.org/mongo-driver/mongo": {},
		"github.com/aws/aws-sdk-go": {},
		"github.com/hashicorp/vault": {},
		"github.com/IBM/sarama": {},
		"github.com/Shopify/sarama": {},
		"k8s.io/client-go": {},
		"log/slog": {},
		"os": {},
		"github.com/aws/aws-sdk-go-v2": {},
		"github.com/redis/go-redis/v9": {},
		"github.com/gocql/gocql": {},
		"cloud.google.com/go/pubsub": {},
		"github.com/99designs/gqlgen": {},
		"github.com/redis/go-redis": {},
		"github.com/graph-gophers/graphql-go": {},
		"github.com/graphql-go/graphql": {},
		"github.com/jackc/pgx": {},
		"github.com/elastic/go-elasticsearch": {},
		"github.com/twitchtv/twirp": {},
		"github.com/segmentio/kafka-go": {},
		"github.com/confluentinc/confluent-kafka-go/kafka": {},
		"github.com/confluentinc/confluent-kafka-go/kafka/v2": {},
		"github.com/julienschmidt/httprouter": {},
		"github.com/sirupsen/logrus": {},
	}
}

func processPackages(packageMap map[string]string) ([]ModuleVersion, error) {
	var modules []ModuleVersion
	for pkg, info := range instrumentation.GetPackages() {
		package_name := string(pkg)
		repository := info.TracedPackage
		packageMap[repository] = package_name

		module, err := GetMinVersion(package_name, repository)
		if err != nil {
			fmt.Printf("Error getting min version for package %s: %v\n", package_name, err)
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
