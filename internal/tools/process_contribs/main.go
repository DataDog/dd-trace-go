package main

//go:generate go run main.go ../../../contrib

import (
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/dstutil"

	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/codegen"
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/entrypoint"
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
	fset := token.NewFileSet()
	dec := decorator.NewDecorator(fset)
	pkgs, err := dec.ParseDir(dir, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse dir: %w", err)
	}

	for name, pkg := range pkgs {
		if strings.HasSuffix(name, "_test") || name == "main" {
			continue
		}
		if err := validatePackage(pkg); err != nil {
			log.Printf("package \"%s.%s\" failed validation: %v\n", dir, name, err)
			continue
		}

		for fPath, f := range pkg.Files {
			fCtx := entrypoint.FunctionContext{
				FilePath:    fPath,
				File:        f,
				Package:     pkg,
				AllPackages: pkgs,
			}
			if err := processFile(fCtx); err != nil {
				log.Fatal(err)
			}
		}
	}
	return nil
}

func processFile(fCtx entrypoint.FunctionContext) error {
	fileUpdates := make(map[string][]codegen.UpdateNodeFunc)

	err := codegen.UpdateFile(fCtx.FilePath, func(cur *dstutil.Cursor) (bool, bool) {
		fn, ok := cur.Node().(*dst.FuncDecl)
		if !ok {
			return false, true
		}

		// no need to iterate deeper after this point
		extraUpdates, changed, err := processFunc(fn, fCtx)
		if err != nil {
			log.Printf("failed to process func %q: %v\n", fn.Name.Name, err)
			return false, false
		}
		for k, v := range extraUpdates {
			fileUpdates[k] = append(fileUpdates[k], v)
		}
		return changed, false
	})
	if err != nil {
		return err
	}

	for f, updates := range fileUpdates {
		for _, u := range updates {
			err := codegen.UpdateFile(f, u)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func processFunc(fn *dst.FuncDecl, fCtx entrypoint.FunctionContext) (map[string]codegen.UpdateNodeFunc, bool, error) {
	comments := fn.Decorations().Start
	name := fn.Name.Name

	for _, raw := range comments {
		comment, ok := entrypoint.ParseComment(raw)
		if !ok {
			continue
		}
		log.Printf("file: %s | function: %s | entrypoint: %s | args: %v\n", fCtx.FilePath, name, comment.Command, comment.Arguments)

		p, ok := entrypoint.AllEntrypoints[comment.Command]
		if !ok {
			return nil, false, fmt.Errorf("unknown ddtrace:entrypoint comment: %s", comment)
		}

		extraUpdates, err := p.Apply(fn, fCtx, comment.Arguments)
		if err != nil {
			return nil, false, err
		}
		return extraUpdates, true, nil
	}
	return nil, false, nil
}

func main() {
	rootDir := "../../../contrib/dimfeld"
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
