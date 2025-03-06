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
	pkgs, err := decorator.ParseDir(fset, dir, nil, parser.ParseComments)
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

		for fPath, _ := range pkg.Files {
			if err := processFile(fPath, pkg); err != nil {
				log.Fatal(err)
			}
		}
	}
	return nil
}

func processFile(fPath string, pkg *dst.Package) error {
	fileUpdates := make(map[string][]updateNodeFunc)

	err := loadAndModifyFile(fPath, func(cur *dstutil.Cursor) (bool, bool) {
		fn, ok := cur.Node().(*dst.FuncDecl)
		if !ok {
			return false, true
		}

		// no need to iterate deeper after this point
		extraUpdates, changed, err := processFunc(fn, pkg, fPath)
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
			err := loadAndModifyFile(f, u)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type Processor interface {
	Apply(fn *dst.FuncDecl, pkg *dst.Package, fPath string, args ...string) (map[string]updateNodeFunc, error)
}

var processors = map[string]Processor{
	"ddtrace:entrypoint:wrap":             entrypointWrap{},
	"ddtrace:entrypoint:modify-struct":    entrypointModifyStruct{},
	"ddtrace:entrypoint:create-hooks":     entrypointCreateHooks{},
	"ddtrace:entrypoint:wrap-custom-type": entrypointWrapCustomType{},
	"ddtrace:entrypoint:ignore":           entrypointIgnore{},
}

func processFunc(fn *dst.FuncDecl, pkg *dst.Package, fPath string) (map[string]updateNodeFunc, bool, error) {
	comments := fn.Decorations().Start
	name := fn.Name.Name

	for _, comment := range comments {
		cmd, args, ok := parseDDTraceEntrypointComment(comment)
		if !ok {
			continue
		}
		log.Printf("file: %s | function: %s | command: %s | args: %v\n", fPath, name, cmd, args)

		p, ok := processors[cmd]
		if !ok {
			return nil, false, fmt.Errorf("unknown ddtrace:entrypoint comment: %s", comment)
		}

		extraUpdates, err := p.Apply(fn, pkg, fPath, args...)
		if err != nil {
			return nil, false, err
		}
		return extraUpdates, true, nil
	}
	return nil, false, nil
}

func parseDDTraceEntrypointComment(comment string) (string, []string, bool) {
	content := strings.TrimPrefix(comment, "//")
	if len(content) == 0 {
		return "", nil, false
	}
	content = strings.TrimLeft(content, " ")
	parts := strings.SplitN(content, " ", 2)
	cmd, args := parts[0], parts[1:]
	if !strings.HasPrefix(cmd, "ddtrace:entrypoint") {
		return "", nil, false
	}
	return cmd, args, true
}

func main() {
	rootDir := "../../../contrib"
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
