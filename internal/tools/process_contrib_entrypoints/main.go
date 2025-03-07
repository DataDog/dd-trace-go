package main

//go:generate go run main.go ../../../contrib

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/decorator/resolver/goast"
	"github.com/dave/dst/decorator/resolver/guess"
	"github.com/dave/dst/dstutil"
	"github.com/hashicorp/go-multierror"
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
			log.Printf("ignoring internal package: %s\n", dir)
			continue
		}
		dirPaths = append(dirPaths, dir)
	}
	slices.Sort(dirPaths)
	return dirPaths, nil
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
			if err := processFile(fPath); err != nil {
				log.Fatal(err)
			}
		}
	}
	return nil
}

func processFile(fPath string) error {
	if strings.HasSuffix(fPath, "_gen.go") {
		return nil
	}

	content, err := os.ReadFile(fPath)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, fPath, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("error parsing content in %s: %w", fPath, err)
	}

	dec := decorator.NewDecoratorWithImports(fset, fPath, goast.New())
	f, err := dec.DecorateFile(astFile)
	if err != nil {
		log.Printf("error decorating file %s (skipping file): %v", fPath, err)
		return nil
	}

	changed := false
	dstutil.Apply(f, func(c *dstutil.Cursor) bool {
		fn, ok := c.Node().(*dst.FuncDecl)
		if !ok {
			return true
		}

		ch, err := processFunc(fn, fPath)
		if err != nil {
			log.Printf("failed to process func %q: %v\n", fn.Name.Name, err)
			return true
		}
		if ch {
			changed = true
		}

		return false
	}, nil)

	if changed {
		restorer := decorator.NewRestorerWithImports(fPath, guess.New())
		var buf bytes.Buffer
		if err := restorer.Fprint(&buf, f); err != nil {
			log.Fatal(err)
		}
		return os.WriteFile(fPath, buf.Bytes(), 0755)
	}

	return nil
}

type Processor interface {
	Apply(fn *dst.FuncDecl, args ...string) error
}

var processors = map[string]Processor{
	"ddtrace:entrypoint:wrap":             entrypointWrap{},
	"ddtrace:entrypoint:modify-struct":    entrypointModifyStruct{},
	"ddtrace:entrypoint:create-hooks":     entrypointCreateHooks{},
	"ddtrace:entrypoint:wrap-custom-type": entrypointWrapCustomType{},
	"ddtrace:entrypoint:ignore":           entrypointIgnore{},
}

func processFunc(fn *dst.FuncDecl, fPath string) (bool, error) {
	allErrors := &multierror.Error{}
	comments := fn.Decorations().Start
	changed := false
	name := fn.Name.Name

	for _, comment := range comments {
		cmd, args, ok := parseDDTraceEntrypointComment(comment)
		if !ok {
			continue
		}
		log.Printf("file: %s | function: %s | command: %s | args: %v\n", fPath, name, cmd, args)

		p, ok := processors[cmd]
		if !ok {
			allErrors = multierror.Append(allErrors, fmt.Errorf("unknown ddtrace:entrypoint comment: %s", comment))
			continue
		}
		if err := p.Apply(fn, args...); err != nil {
			allErrors = multierror.Append(allErrors, err)
		} else {
			changed = true
		}
	}
	return changed, allErrors.ErrorOrNil()
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
