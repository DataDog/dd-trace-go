// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

type registryBinding struct {
	id       string
	consumer string
	keys     []string
	sampling uint64
}

type registryRaw struct {
	key       string
	sources   uint64
	telemetry uint64
}

type declarationRegistry struct {
	rawKeys  map[string]registryRaw
	bindings map[string]registryBinding
}

func newDeclarationRegistry() *declarationRegistry {
	return &declarationRegistry{
		rawKeys:  make(map[string]registryRaw),
		bindings: make(map[string]registryBinding),
	}
}

func (r *declarationRegistry) addRaw(raw registryRaw) error {
	if _, exists := r.rawKeys[raw.key]; exists {
		return fmt.Errorf("duplicate raw key %q", raw.key)
	}
	r.rawKeys[raw.key] = raw
	return nil
}

func (r *declarationRegistry) addBinding(binding registryBinding) error {
	if _, exists := r.bindings[binding.id]; exists {
		return fmt.Errorf("duplicate binding ID %q", binding.id)
	}
	r.bindings[binding.id] = binding
	return nil
}

func (r *declarationRegistry) validate() error {
	bound := make(map[string]struct{}, len(r.rawKeys))
	keys := make([]string, 0, len(r.rawKeys))
	for key := range r.rawKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw := r.rawKeys[key]
		if raw.key == "" {
			return fmt.Errorf("raw definition key must not be empty")
		}
		if raw.sources > 1 {
			return fmt.Errorf("raw key %q has invalid source policy %d", key, raw.sources)
		}
		if raw.telemetry > 3 {
			return fmt.Errorf("raw key %q has invalid telemetry policy %d", key, raw.telemetry)
		}
	}

	ids := make([]string, 0, len(r.bindings))
	for id := range r.bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		binding := r.bindings[id]
		if binding.id == "" {
			return fmt.Errorf("consumer binding ID must not be empty")
		}
		if binding.consumer == "" {
			return fmt.Errorf("binding %q has an empty consumer", id)
		}
		if len(binding.keys) == 0 {
			return fmt.Errorf("binding %q has no raw keys", id)
		}
		if binding.sampling > 5 {
			return fmt.Errorf("binding %q has invalid sampling boundary %d", id, binding.sampling)
		}
		for _, key := range binding.keys {
			if _, exists := r.rawKeys[key]; !exists {
				return fmt.Errorf("binding %q references unregistered raw key %q", id, key)
			}
			bound[key] = struct{}{}
		}
	}
	for _, key := range keys {
		if _, exists := bound[key]; !exists {
			return fmt.Errorf("raw key %q has no consumer binding", key)
		}
	}
	return nil
}

func loadRegistry(pkgs []*packages.Package) (*declarationRegistry, error) {
	registry := newDeclarationRegistry()
	declarations := 0
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			accepted := make(map[*ast.CallExpr]struct{})
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != "init" || function.Recv != nil {
					continue
				}
				registrationPrefix := true
				for _, statement := range function.Body.List {
					call, name, ok := directRegistryCall(pkg, statement)
					if !ok {
						registrationPrefix = false
						continue
					}
					if !registrationPrefix {
						return nil, registryInitializationError(name.Name)
					}
					accepted[call] = struct{}{}
					declarations++
					var err error
					switch name.Name {
					case "registerRaw":
						err = parseRawDeclaration(registry, pkg.TypesInfo, call)
					case "registerBinding":
						err = parseBindingDeclaration(registry, pkg.TypesInfo, call)
					}
					if err != nil {
						return nil, err
					}
				}
			}

			var invalidCall *ast.Ident
			ast.Inspect(file, func(node ast.Node) bool {
				if invalidCall != nil {
					return false
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := registryHelperCall(pkg, call)
				if !ok {
					return true
				}
				if _, ok := accepted[call]; ok {
					return true
				}
				invalidCall = name
				return false
			})
			if invalidCall != nil {
				return nil, registryInitializationError(invalidCall.Name)
			}
		}
	}
	if declarations == 0 {
		return nil, errorsNoRegistryDeclarations
	}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	return registry, nil
}

func directRegistryCall(pkg *packages.Package, statement ast.Stmt) (*ast.CallExpr, *ast.Ident, bool) {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return nil, nil, false
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	name, ok := registryHelperCall(pkg, call)
	return call, name, ok
}

func registryHelperCall(pkg *packages.Package, call *ast.CallExpr) (*ast.Ident, bool) {
	name, ok := call.Fun.(*ast.Ident)
	if !ok || (name.Name != "registerRaw" && name.Name != "registerBinding") {
		return nil, false
	}
	if !isRegistryHelper(pkg, name) {
		return nil, false
	}
	return name, true
}

func registryInitializationError(helper string) error {
	return fmt.Errorf("%s registration must be a direct statement in the registration prefix of func init", helper)
}

func isRegistryHelper(pkg *packages.Package, ident *ast.Ident) bool {
	fn, ok := pkg.TypesInfo.Uses[ident].(*types.Func)
	if !ok || fn.Pkg() != pkg.Types {
		return false
	}
	return pkg.Types.Scope().Lookup(ident.Name) == fn
}

var errorsNoRegistryDeclarations = fmt.Errorf("no registerRaw or registerBinding declarations found")

func parseRawDeclaration(registry *declarationRegistry, info *types.Info, call *ast.CallExpr) error {
	literal, err := registryLiteral(call, "raw definition")
	if err != nil {
		return err
	}
	keyExpr, ok := literalField(literal, "Key")
	if !ok {
		return fmt.Errorf("raw definition key must be constant")
	}
	key, ok := resolveStringArg(info, keyExpr)
	if !ok {
		return fmt.Errorf("raw definition key must be constant")
	}
	sourcesExpr, ok := literalField(literal, "Sources")
	if !ok {
		return fmt.Errorf("raw definition source policy must be constant")
	}
	sources, ok := resolveUintArg(info, sourcesExpr)
	if !ok {
		return fmt.Errorf("raw definition source policy must be constant")
	}
	telemetryExpr, ok := literalField(literal, "Telemetry")
	if !ok {
		return fmt.Errorf("raw definition telemetry policy must be constant")
	}
	telemetry, ok := resolveUintArg(info, telemetryExpr)
	if !ok {
		return fmt.Errorf("raw definition telemetry policy must be constant")
	}
	return registry.addRaw(registryRaw{
		key:       key,
		sources:   sources,
		telemetry: telemetry,
	})
}

func parseBindingDeclaration(registry *declarationRegistry, info *types.Info, call *ast.CallExpr) error {
	literal, err := registryLiteral(call, "consumer binding")
	if err != nil {
		return err
	}
	idExpr, ok := literalField(literal, "ID")
	if !ok {
		return fmt.Errorf("consumer binding ID must be constant")
	}
	id, ok := resolveStringArg(info, idExpr)
	if !ok {
		return fmt.Errorf("consumer binding ID must be constant")
	}
	consumerExpr, ok := literalField(literal, "Consumer")
	if !ok {
		return fmt.Errorf("consumer binding consumer must be constant")
	}
	consumer, ok := resolveStringArg(info, consumerExpr)
	if !ok {
		return fmt.Errorf("consumer binding consumer must be constant")
	}
	keysExpr, ok := literalField(literal, "Keys")
	if !ok {
		return fmt.Errorf("consumer binding %q has no raw keys", id)
	}
	keysLiteral, ok := keysExpr.(*ast.CompositeLit)
	if !ok {
		return fmt.Errorf("consumer binding %q keys must be a literal", id)
	}
	keys := make([]string, 0, len(keysLiteral.Elts))
	for _, element := range keysLiteral.Elts {
		expr := element
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			expr = keyed.Value
		}
		key, ok := resolveStringArg(info, expr)
		if !ok {
			return fmt.Errorf("consumer binding key must be constant")
		}
		keys = append(keys, key)
	}
	samplingExpr, ok := literalField(literal, "Sampling")
	if !ok {
		return fmt.Errorf("consumer binding sampling boundary must be constant")
	}
	sampling, ok := resolveUintArg(info, samplingExpr)
	if !ok {
		return fmt.Errorf("consumer binding sampling boundary must be constant")
	}
	return registry.addBinding(registryBinding{
		id:       id,
		consumer: consumer,
		keys:     keys,
		sampling: sampling,
	})
}

func registryLiteral(call *ast.CallExpr, declaration string) (*ast.CompositeLit, error) {
	if len(call.Args) != 1 {
		return nil, fmt.Errorf("%s registration must have one argument", declaration)
	}
	literal, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("%s registration must use a composite literal", declaration)
	}
	return literal, nil
}

func literalField(literal *ast.CompositeLit, name string) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := field.Key.(*ast.Ident)
		if ok && ident.Name == name {
			return field.Value, true
		}
	}
	return nil, false
}
