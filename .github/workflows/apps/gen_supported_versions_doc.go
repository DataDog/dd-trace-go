// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

const outputPath = "./contrib/supported_versions.md"

// TODO: currently this is taken from the https://github.com/DataDog/orchestrion README, it will be fetched dynamically
// when Orchestrion aspects are moved to dd-trace-go.
var autoInstrumentedLibs = map[string]struct{}{
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

// stdlibPackages are used to skip in version checking.
var stdlibPackages = map[string]struct{}{
	"log/slog":     {},
	"os":           {},
	"net/http":     {},
	"database/sql": {},
}

type ModuleVersion struct {
	Name           string
	MinVersion     string
	MaxVersion     string
	Repository     string
	isInstrumented bool
}

// modUpdate is the type returned by 'go list -m -u -json <module>'
type modUpdate struct {
	Path    string
	Version string
	Update  struct {
		Path    string
		Version string
	}
}

func main() {
	modules, err := processPackages()
	if err != nil {
		log.Fatalf("Error processing packages: %v\n", err)
	}

	// update with instrumented status
	for i := range modules {
		modules[i].isInstrumented = isModuleAutoInstrumented(modules[i].Repository)
	}

	modulesWithLatest := fetchAllLatestVersions(modules)

	if err := writeMarkdownFile(modulesWithLatest, outputPath); err != nil {
		fmt.Println(err)
	}

	fmt.Println("Version information written to", outputPath)
}

func fetchLatestVersion(module string) (string, error) {
	if _, ok := stdlibPackages[module]; ok {
		return "N/A", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Run `go list -m -u -json <module>` to retrieve latest available upgrade
	out, err := runCommand(ctx, "", "go", "list", "-m", "-u", "-json", module)
	if err != nil {
		return "", err
	}

	var m modUpdate
	if err := json.Unmarshal(out, &m); err != nil {
		return "", fmt.Errorf("unexpected 'go list -m -u -json' output: %v", err)
	}

	latest := m.Version
	if m.Update.Version != "" {
		latest = m.Update.Version
	}
	return latest, nil
}

// isModuleAutoInstrumented returns whether the module has automatic tracing supported (by Orchestrion)
func isModuleAutoInstrumented(moduleName string) bool {
	for key := range autoInstrumentedLibs {
		if isSubdirectory(moduleName, key) {
			return true
		}
	}
	return false
}

func isSubdirectory(url, pattern string) bool {
	if strings.HasPrefix(url, pattern) {
		// match is either exact or followed by a "/"
		return len(url) == len(pattern) || url[len(pattern)] == '/'
	}
	return false
}

// getCurrentVersion parses the go.mod file for a package and extracts the version of a given repository.
func getCurrentVersion(integrationName, modName string) (ModuleVersion, error) {
	if _, ok := stdlibPackages[integrationName]; ok {
		return ModuleVersion{
			Name:           integrationName,
			MinVersion:     "N/A",
			MaxVersion:     "N/A",
			Repository:     modName,
			isInstrumented: false,
		}, nil
	}

	// Path to contrib/{packageName}
	contribPath := filepath.Join("contrib", integrationName)
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
	repoPattern := fmt.Sprintf(`\b%s\b`, strings.ReplaceAll(modName, "/", `/`))
	repoRegex, err := regexp.Compile(repoPattern)
	if err != nil {
		return ModuleVersion{}, fmt.Errorf("invalid repository regex pattern: %w", err)
	}

	// Iterate through require dependencies
	for _, req := range f.Require {
		if repoRegex.MatchString(req.Mod.Path) {
			return ModuleVersion{
				Name:           integrationName,
				MinVersion:     req.Mod.Version,
				MaxVersion:     "",
				Repository:     req.Mod.Path,
				isInstrumented: false,
			}, nil
		}
	}
	return ModuleVersion{}, fmt.Errorf("repository %s not found in go.mod", modName)
}

// fetchAllLatestVersions concurrently fetches the latest version of each module.
func fetchAllLatestVersions(modules []ModuleVersion) []ModuleVersion {
	var wg sync.WaitGroup

	updatedModules := make([]ModuleVersion, len(modules))

	wg.Add(len(modules))
	for i, mod := range modules {
		go func(i int, mod ModuleVersion) {
			defer wg.Done()
			latestVersion, err := fetchLatestVersion(mod.Repository)
			if err != nil {
				fmt.Printf("Error fetching latest version for %s: %v\n", mod.Repository, err)
				updatedModules[i] = ModuleVersion{mod.Name, mod.MinVersion, "Error", mod.Repository, mod.isInstrumented}
				return
			}

			updatedModules[i] = ModuleVersion{
				Name:           mod.Name,
				MinVersion:     mod.MinVersion,
				MaxVersion:     latestVersion,
				Repository:     mod.Repository,
				isInstrumented: mod.isInstrumented,
			}
		}(i, mod)
	}

	wg.Wait()
	return updatedModules
}

func writeMarkdownFile(modules []ModuleVersion, filePath string) error {
	// Sort modules by name
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Name < modules[j].Name
	})

	maxColumnLength := []int{0, 0, 0, 0, 0}

	rows := [][]string{
		{"Module", "Datadog Integration", "Minimum Tested Version", "Maximum Tested Version", "Auto-Instrumented"},
		{"-", "-", "-", "-", "-"},
	}
	for _, mod := range modules {
		rows = append(rows, []string{
			modWithPkgDevURL(mod.Repository, mod.Repository),
			integrationWithPackageURL(mod.Name),
			fmt.Sprintf("`%s`", mod.MinVersion),
			fmt.Sprintf("`%s`", mod.MaxVersion),
			boolToMarkdown(mod.isInstrumented),
		})
	}
	for _, row := range rows {
		for i, col := range row {
			if len(col) > maxColumnLength[i] {
				maxColumnLength[i] = len(col)
			}
		}
	}
	for _, row := range rows {
		for i, col := range row {
			char := " "
			if col == "-" {
				char = "-"
			}
			if len(col) < maxColumnLength[i] {
				row[i] = row[i] + strings.Repeat(char, maxColumnLength[i]-len(col))
			}
			row[i] = char + row[i] + char
		}
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("failed to closed file: %v\n", err)
		}
	}()

	for _, row := range rows {
		if _, err := fmt.Fprintln(file, "|"+strings.Join(row, "|")+"|"); err != nil {
			return fmt.Errorf("failed to write line: %w", err)
		}
	}
	return nil
}

func processPackages() ([]ModuleVersion, error) {
	var modules []ModuleVersion
	for integrationName, mod := range instrumentation.GetPackages() {
		modName := mod.TracedPackage
		module, err := getCurrentVersion(string(integrationName), modName)
		if err != nil {
			return nil, err
		}
		modules = append(modules, modName)
	}
	return modules, nil

}

func runCommand(ctx context.Context, dir string, commandAndArgs ...string) ([]byte, error) {
	log.Printf("running command: %q\n", strings.Join(commandAndArgs, " "))

	cmd := exec.CommandContext(ctx, commandAndArgs[0], commandAndArgs[1:]...)
	cmd.Stderr = os.Stderr
	cmd.Dir = dir

	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run command %q: %v", strings.Join(commandAndArgs, " "), err)
	}
	return b, nil
}

func integrationWithPackageURL(integrationName string) string {
	modURL := fmt.Sprintf("github.com/DataDog/dd-trace-go/contrib/%s/v2", integrationName)
	return modWithPkgDevURL("contrib/"+integrationName, modURL)
}

func modWithPkgDevURL(name, modURL string) string {
	return fmt.Sprintf("[%s](https://pkg.go.dev/%s)", name, modURL)
}

func boolToMarkdown(val bool) string {
	if val {
		return ":white_check_mark:"
	}
	return " "
}
