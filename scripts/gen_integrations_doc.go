// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build ignore
// +build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"golang.org/x/mod/modfile"
)

const outputPath = "./contrib/supported_integrations.md"

// stdlibPackages are used to skip in version checking.
var stdlibPackages = map[string]struct{}{
	"log/slog":     {},
	"os":           {},
	"net/http":     {},
	"database/sql": {},
}

type integration struct {
	name                 string
	minVersion           string
	maxVersion           string
	repository           string
	orchestrionSupported bool
	packages             map[string]integrationPackage
}

type integrationPackage struct {
	name           string
	importPath     string
	docString      string
	deprecated     bool
	deprecationDoc string
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
	integrations, err := fetchIntegrations()
	if err != nil {
		log.Fatalf("failed to fetch integrations: %v\n", err)
	}
	if err := writeMarkdownFile(integrations, outputPath); err != nil {
		log.Fatalf("failed to write markdown file: %v\n", err)
	}
	log.Printf("integration docs has been written to: %s\n", outputPath)
}

func fetchIntegrations() ([]integration, error) {
	pkgs := instrumentation.GetPackages()

	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		integrations = make([]integration, 0, len(pkgs))
	)
	for name, mod := range pkgs {
		wg.Add(1)

		integrationName := name
		modName := mod.TracedPackage

		go func() {
			defer wg.Done()

			ig, err := fetchIntegrationInfo(string(integrationName), modName)
			if err != nil {
				log.Printf("WARN: failed to read integration info: %v\n", err)
				return
			}

			mu.Lock()
			integrations = append(integrations, ig)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return integrations, nil
}

func fetchIntegrationInfo(name, tracedPackage string) (integration, error) {
	// Path to contrib/{packageName}
	contribPath := filepath.Join("contrib", name)
	goModPath := filepath.Join(contribPath, "go.mod")

	// Check if go.mod exists in directory
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		return integration{}, fmt.Errorf("go.mod not found in %s", contribPath)
	}

	packages, err := parseIntegrationPackages(contribPath)
	if err != nil {
		return integration{}, fmt.Errorf("failed to fetch integration packages: %v", err)
	}

	orchestrionSupported := false
	for _, pkg := range packages {
		aspectPath := path.Join(contribPath, pkg.importPath, "orchestrion.yml")
		if _, err := os.Stat(aspectPath); err == nil {
			orchestrionSupported = true
			break
		}
	}

	var (
		minVersion = "N/A"
		maxVersion = "N/A"
		repository = tracedPackage
	)
	if _, ok := stdlibPackages[name]; !ok {
		minVersion, maxVersion, repository, err = fetchVersionInfo(goModPath, tracedPackage)
		if err != nil {
			return integration{}, fmt.Errorf("failed to fetch version info: %v", err)
		}
	}
	return integration{
		name:                 name,
		minVersion:           minVersion,
		maxVersion:           maxVersion,
		repository:           repository,
		orchestrionSupported: orchestrionSupported,
		packages:             packages,
	}, nil
}

func fetchVersionInfo(goModPath, tracedPackage string) (minVersion, maxVersion, repository string, resultErr error) {
	// Read the go.mod
	data, err := os.ReadFile(goModPath)
	if err != nil {
		resultErr = fmt.Errorf("failed to read go.mod: %w", err)
		return
	}

	// Parse the go.mod
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		resultErr = fmt.Errorf("failed to parse go.mod: %w", err)
		return
	}

	// match the repository name
	repoPattern := fmt.Sprintf(`\b%s\b`, strings.ReplaceAll(tracedPackage, "/", `/`))
	repoRegex, err := regexp.Compile(repoPattern)
	if err != nil {
		resultErr = fmt.Errorf("invalid repository regex pattern: %w", err)
		return
	}

	// Iterate through require dependencies
	for _, req := range f.Require {
		if repoRegex.MatchString(req.Mod.Path) {
			latestVersion, err := fetchLatestVersion(req.Mod.Path)
			if err != nil {
				log.Printf("error fetching latest version for %s: %v\n", req.Mod.Path, err)
			}
			return req.Mod.Version, latestVersion, req.Mod.Path, nil

		}
	}
	resultErr = fmt.Errorf("repository %s not found in go.mod", tracedPackage)
	return
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

func writeMarkdownFile(integrations []integration, filePath string) error {
	sort.Slice(integrations, func(i, j int) bool { return integrations[i].name < integrations[j].name })

	var (
		nonDeprecated []integration
		deprecated    []integration
	)
	for _, ig := range integrations {
		isDeprecated := true
		for _, pkg := range ig.packages {
			if !pkg.deprecated {
				isDeprecated = false
				break
			}
		}
		if isDeprecated {
			deprecated = append(deprecated, ig)
		} else {
			nonDeprecated = append(nonDeprecated, ig)
		}
	}

	buildTable := func(rows [][]string) [][]string {
		if len(rows) == 0 {
			return rows
		}
		max := make([]int, len(rows[0]))
		for _, r := range rows {
			for i, col := range r {
				if l := len(col); l > max[i] {
					max[i] = l
				}
			}
		}
		for _, r := range rows {
			for i, col := range r {
				pad := " "
				if col == "-" {
					pad = "-"
				}
				if n := max[i] - len(col); n > 0 {
					r[i] += strings.Repeat(pad, n)
				}
				r[i] = pad + r[i] + pad
			}
		}
		return rows
	}

	header := []string{
		"Package", "Datadog Integration", "Minimum Tested Version",
		"Maximum Tested Version", "Orchestrion support",
	}
	sep := []string{"-", "-", "-", "-", "-"}

	rowsActive := [][]string{header, sep}
	for _, ig := range nonDeprecated {
		rowsActive = append(rowsActive, []string{
			modWithPkgDevURL(ig.repository, ig.repository),
			integrationWithPackageURL(ig.name),
			fmt.Sprintf("`%s`", ig.minVersion),
			fmt.Sprintf("`%s`", ig.maxVersion),
			boolToMarkdown(ig.orchestrionSupported),
		})
	}
	rowsActive = buildTable(rowsActive)

	headerDep := append(append([]string{}, header...), "Deprecation comment")
	sepDep := []string{"-", "-", "-", "-", "-", "-"}
	rowsDep := [][]string{headerDep, sepDep}
	for _, ig := range deprecated {
		rowsDep = append(rowsDep, []string{
			modWithPkgDevURL(ig.repository, ig.repository),
			integrationWithPackageURL(ig.name),
			fmt.Sprintf("`%s`", ig.minVersion),
			fmt.Sprintf("`%s`", ig.maxVersion),
			boolToMarkdown(ig.orchestrionSupported),
			deprecationCommentMarkdown(filepath.Join("contrib", ig.name), ig.packages),
		})
	}
	rowsDep = buildTable(rowsDep)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			log.Printf("failed to close file: %v\n", cerr)
		}
	}()

	for _, row := range rowsActive {
		if _, err := fmt.Fprintln(file, "|"+strings.Join(row, "|")+"|"); err != nil {
			return fmt.Errorf("failed to write active table: %w", err)
		}
	}

	if len(deprecated) > 0 {
		if _, err := fmt.Fprintln(file, "\n⚠️ The following integrations are marked as deprecated and will be removed in future releases. ⚠️\n"); err != nil {
			return fmt.Errorf("failed to write deprecation note: %w", err)
		}
		for _, row := range rowsDep {
			if _, err := fmt.Fprintln(file, "|"+strings.Join(row, "|")+"|"); err != nil {
				return fmt.Errorf("failed to write deprecated table: %w", err)
			}
		}
	}

	return nil
}

func parseIntegrationPackages(contribPath string) (map[string]integrationPackage, error) {
	pkgs := make(map[string]integrationPackage)

	err := filepath.WalkDir(contribPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" {
			return filepath.SkipDir
		}
		parts := strings.Split(path, string(filepath.Separator))
		for _, part := range parts {
			if part == "internal" {
				return filepath.SkipDir
			}
		}
		pkg, err := build.Default.ImportDir(path, 0)
		if err != nil {
			return nil
		}
		if pkg.Name == "main" {
			return nil
		}
		fset := token.NewFileSet()
		var files []*ast.File

		for _, filename := range pkg.GoFiles {
			fullPath := filepath.Join(pkg.Dir, filename)
			f, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
			if err != nil {
				continue // skip invalid files
			}
			files = append(files, f)
		}

		if len(files) == 0 {
			return nil
		}

		apkg, err := doc.NewFromFiles(fset, files, path)
		if err != nil {
			return fmt.Errorf("failed to compute documentation for package: %v", err)
		}

		docStr := apkg.Doc
		deprecated, deprecation := deprecationInfo(apkg.Doc)

		relImportPath, err := filepath.Rel(contribPath, path)
		if err != nil {
			relImportPath = path
		}
		pkgs[relImportPath] = integrationPackage{
			name:           pkg.Name,
			importPath:     relImportPath,
			docString:      docStr,
			deprecated:     deprecated,
			deprecationDoc: deprecation,
		}
		return nil
	})

	return pkgs, err
}

// deprecationInfo scans a *go/doc* package‐level docString string and,
// if it contains a "Deprecated:" section, returns (true, message).
func deprecationInfo(docStr string) (bool, string) {
	const tag = "Deprecated:"
	idx := strings.Index(docStr, tag)
	if idx == -1 {
		return false, ""
	}

	msg := strings.TrimLeft(docStr[idx+len(tag):], " \t\n")

	var lines []string
	for _, line := range strings.Split(msg, "\n") {
		if strings.TrimSpace(line) == "" {
			break
		}
		lines = append(lines, strings.TrimSpace(line))
	}

	return true, strings.Join(lines, " ")
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

func deprecationCommentMarkdown(contribPath string, pkgs map[string]integrationPackage) string {
	var entries []struct {
		name string
		doc  string
	}
	for _, pkg := range pkgs {
		if pkg.deprecated {
			deprecationDoc := strings.TrimSpace(pkg.deprecationDoc)
			if deprecationDoc == "" {
				deprecationDoc = "This package is deprecated."
			}
			entries = append(entries, struct {
				name string
				doc  string
			}{path.Join(contribPath, pkg.importPath), deprecationDoc})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var b strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&b, "- `%s`: %s\n", entry.name, entry.doc)
	}
	return strings.TrimSpace(b.String())
}
