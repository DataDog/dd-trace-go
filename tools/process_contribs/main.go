package main

//go:generate go run main.go ../../../contrib

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dave/dst"

	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/comment"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/entrypoint"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
)

func getGoDirs(rootDir string) ([]string, error) {
	paths := make(map[string]struct{})
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
			paths[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirPaths := make([]string, 0, len(paths))
	for dir, _ := range paths {
		if strings.Contains(dir, "/internal") {
			// ignore internal packages
			continue
		}
		dirPaths = append(dirPaths, dir)
	}
	sort.Slice(dirPaths, func(i, j int) bool {
		partsI := splitPath(strings.ToLower(dirPaths[i]))
		partsJ := splitPath(strings.ToLower(dirPaths[j]))

		// Compare each part of the path recursively
		for k := 0; k < len(partsI) && k < len(partsJ); k++ {
			if partsI[k] != partsJ[k] {
				return partsI[k] < partsJ[k]
			}
		}

		// If one path is a subpath of the other, the shorter one should come first
		return len(partsI) < len(partsJ)
	})
	return dirPaths, nil
}

// splitPath splits a path into its components (directories and filename)
func splitPath(p string) []string {
	return strings.Split(strings.Trim(filepath.Clean(p), "/"), "/")
}

func processDir(dir string) error {
	pkg, err := typechecker.LoadPackage(dir)
	if err != nil {
		return err
	}

	if err := pkg.Validate(); err != nil {
		log.Printf("[package: %s] package %q failed validation: %v\n", pkg.Path(), pkg.Name(), err)
		return nil
	}

	for _, fn := range pkg.Functions(false) {
		e, c, ok := getEntrypoint(fn.Node)
		if !ok {
			continue
		}
		changes, err := e.Apply(fn, pkg, c.Arguments)
		if err != nil {
			log.Printf("[package: %s | function: %s | entrypoint: %s] failed to apply entrypoint: %v", pkg.Path(), fn.Type.Name(), c.Command, err)
			continue
		}
		if changes != nil {
			log.Printf("[package: %s | function: %s | entrypoint: %s] succesfully applied changes", pkg.Path(), fn.Type.Name(), c.Command)
			pkg.ApplyChanges(changes)
		}
	}
	if err := pkg.Save(); err != nil {
		return err
	}

	return nil
}

func getEntrypoint(fn *dst.FuncDecl) (entrypoint.Entrypoint, comment.Comment, bool) {
	comments := fn.Decorations().Start

	for _, raw := range comments {
		c, ok := comment.ParseComment(raw)
		if !ok {
			continue
		}

		p, ok := entrypoint.AllEntrypoints[c.Command]
		if !ok {
			log.Printf("unknown ddtrace:entrypoint comment: %s", raw)
			continue
		}
		return p, c, true
	}
	return nil, comment.Comment{}, false
}

func main() {
	rootDir := "../../../contrib/confluentinc"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "" {
		rootDir = args[0]
	}

	goDirs, err := getGoDirs(rootDir)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range goDirs {
		if err := processDir(dir); err != nil {
			log.Fatal(err)
		}
	}
}
