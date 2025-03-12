package main

import (
	"errors"
	"fmt"
	"github.com/dave/dst"
	"github.com/hashicorp/go-multierror"
	"go/token"
	"strings"
	"unicode"
)

func validatePackage(pkg *dst.Package) error {
	err := &multierror.Error{}

	var allConsts []*dst.GenDecl
	var allPublicFuncs []*dst.FuncDecl
	for fName, f := range pkg.Files {
		if strings.HasSuffix(fName, "_test.go") || strings.HasSuffix(fName, "_example.go") {
			continue
		}
		for _, decl := range f.Decls {
			switch t := decl.(type) {
			case *dst.FuncDecl:
				shouldSkip := !unicode.IsUpper(rune(t.Name.Name[0])) || // ignore private functions
					//strings.HasPrefix(t.Name.Name, "Test") || // ignore tests
					//strings.HasPrefix(t.Name.Name, "Example") || // ignore examples
					//strings.HasPrefix(t.Name.Name, "Benchmark") || // ignore examples
					t.Recv != nil || // ignore methods
					isFunctionalOption(t) // ignore functional options

				if shouldSkip {
					continue
				}
				allPublicFuncs = append(allPublicFuncs, t)

			case *dst.GenDecl:
				if t.Tok == token.CONST {
					allConsts = append(allConsts, t)
				}
			}
		}
	}

	foundComponentName := false
	for _, c := range allConsts {
		if isTargetConst(c, "componentName") {
			foundComponentName = true
			break
		}
	}
	if !foundComponentName {
		err = multierror.Append(err, errors.New("const componentName package-level declaration not found"))
	}

	for _, fn := range allPublicFuncs {
		foundEntrypointComment := false
		comments := fn.Decorations().Start
		for _, c := range comments {
			_, _, ok := parseDDTraceEntrypointComment(c)
			if ok {
				if foundEntrypointComment {
					err = multierror.Append(err, fmt.Errorf("public function %s has multiple entrypoint comments", fn.Name.Name))
					break
				} else {
					foundEntrypointComment = true
				}
			}
		}
		if !foundEntrypointComment {
			err = multierror.Append(err, fmt.Errorf("public function %s does not have entrypoint comment", fn.Name.Name))
		}
	}

	return err.ErrorOrNil()
}

func isTargetConst(decl *dst.GenDecl, targetName string) bool {
	if decl.Tok != token.CONST {
		return false
	}
	if len(decl.Specs) == 0 {
		return false
	}
	spec := decl.Specs[0]
	valueSpec, ok := spec.(*dst.ValueSpec)
	if !ok {
		return false
	}
	if len(valueSpec.Names) == 0 {
		return false
	}
	name := valueSpec.Names[0].Name
	return name == targetName
}

func isFunctionalOption(fn *dst.FuncDecl) bool {
	s := getFunctionSignature(fn)
	// functional options return exactly 1 result
	if len(s.Returns) != 1 {
		return false
	}
	ret := s.Returns[0]
	return strings.Contains(strings.ToLower(ret.Type), "option")
}
