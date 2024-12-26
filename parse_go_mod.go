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
	"regexp"
	"strings"
	"sync"
	"time"
)

type ModuleVersion struct {
	Name        string
	MinVersion  string
	MaxVersion  string
	isInstrumented bool

}

func parseGoMod(filePath string) ([]ModuleVersion, error) {
	// This parses the go.mod file and extracts modules with their minimum versions.
	var modules []ModuleVersion

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error: %s not found", filePath)
	}
	defer file.Close()

	regex := regexp.MustCompile(`^\s*([^\s]+)\s+v([0-9]+\.[0-9]+\.[0-9]+.*)`)

	scanner := bufio.NewScanner(file)
	inRequireBlock := false

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

		if inRequireBlock || strings.HasPrefix(line, "require ") {
			match := regex.FindStringSubmatch(line)
			if match != nil {
				modules = append(modules, ModuleVersion{
					Name:       match[1],
					MinVersion: match[2],
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	return modules, nil
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
	fmt.Println("Versions:", versions)
	if len(versions) < 1 {
		return "", fmt.Errorf("no versions found for module: %s", module)
	}

	return versions[len(versions)-1], nil
}

func fetchAllLatestVersions(modules []ModuleVersion, instrumentedSet map[string]struct{}) []ModuleVersion {
	// Concurrently fetches the latest version of each module.
		
	var wg sync.WaitGroup
	var mu sync.Mutex

	updatedModules := make([]ModuleVersion, 0, len(modules))

	wg.Add(len(modules))
	for _, mod := range modules {
		go func(mod ModuleVersion) {
			defer wg.Done()
			fmt.Printf("Fetching latest version for module: %s\n", mod.Name)
			latestVersion, err := fetchLatestVersion(mod.Name)
			if err != nil {
				fmt.Printf("Error fetching latest version for %s: %v\n", mod.Name, err)
				mu.Lock()
				updatedModules = append(updatedModules, ModuleVersion{mod.Name, mod.MinVersion, "Error", false})
				mu.Unlock()
				return
			}
			_, isInstrumented := instrumentedSet[mod.Name]

			mu.Lock()
			updatedModules = append(updatedModules, ModuleVersion{mod.Name, 
				mod.MinVersion, latestVersion, isInstrumented})
			mu.Unlock()
		}(mod)
	}

	wg.Wait()
	return updatedModules
}

func outputVersionsAsMarkdown(modules []ModuleVersion, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer file.Close()

	fmt.Fprintln(file, "| Dependency | Minimum Version | Maximum Version | Auto-Instrumented |")
	fmt.Fprintln(file, "|------------|-----------------|-----------------|-----------------|")
	for _, mod := range modules {
		fmt.Fprintf(file, "| %s | v%s | %s | %+v\n", mod.Name, mod.MinVersion, mod.MaxVersion, mod.isInstrumented)
	}
	return nil
}

func main() {
	goModPath := "integration_go.mod" // Modify path as needed
	outputPath := "supported_versions.md"
	instrumentedSet := map[string]struct{}{
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
		"github.com/aws-sdk-go/aws": {},
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

	modules, err := parseGoMod(goModPath)
	if err != nil {
		fmt.Println(err)
		return
	}

	modulesWithLatest := fetchAllLatestVersions(modules, instrumentedSet)

	err = outputVersionsAsMarkdown(modulesWithLatest, outputPath)
	if err != nil {
		fmt.Printf("Error writing output file: %v\n", err)
		return
	}

	fmt.Println("Version information written to", outputPath)
}
