//go:build ignore

// apicheck prints the exported constants, functions, and types from a package.
package main

import (
	"fmt"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	c := packages.Config{
		Mode: packages.NeedTypes,
	}
	p, err := packages.Load(&c, os.Args[1])
	if err != nil {
		panic(err)
	}
	var api []APIEntry
	for _, pkg := range p {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			api = append(api, CreateAPIEntry(obj, scope))
		}
	}

	sort.Slice(api, func(i, j int) bool {
		return api[i].Name < api[j].Name
	})

	for _, e := range api {
		fmt.Println(e.Name)
		sort.Strings(e.Methods)
		for _, m := range e.Methods {
			fmt.Println(m)
		}
	}
}

type APIEntry struct {
	Name    string
	Methods []string
}

func CreateAPIEntry(obj types.Object, scope *types.Scope) APIEntry {
	qual := func(pkg *types.Package) string {
		// Qualify types within this package using just the package
		// name, rather than the whole path, just so the output is less
		// verbose
		//return pkg.Name()
		return ""
	}
	b := new(strings.Builder)
	fmt.Fprint(b, types.ObjectString(obj, qual))
	if c, ok := obj.(*types.Const); ok {
		fmt.Fprintf(b, " = %s", c.Val())
	}
	api := APIEntry{
		Name: b.String(),
	}
	if _, ok := obj.(*types.TypeName); ok {
		// Value receiver methods
		valueMethods := make(map[string]struct{})
		methods := types.NewMethodSet(obj.Type())
		for i := 0; i < methods.Len(); i++ {
			s := methods.At(i)
			api.Methods = append(api.Methods, types.SelectionString(s, qual))
			valueMethods[s.Obj().Name()] = struct{}{}
		}
		// Pointer receiver methods
		methods = types.NewMethodSet(types.NewPointer(obj.Type()))
		for i := 0; i < methods.Len(); i++ {
			s := methods.At(i)
			if _, ok := valueMethods[s.Obj().Name()]; ok {
				// A *T can use all of T's value-receiver
				// methods, so we see the value methods here as
				// well. Don't show them here since they were
				// already shown above
				continue
			}
			api.Methods = append(api.Methods, types.SelectionString(s, qual))
		}
	}
	return api
}
