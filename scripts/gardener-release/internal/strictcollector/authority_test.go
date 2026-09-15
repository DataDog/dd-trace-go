// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const auditedSettleFixedBody = `{
	var mode readMode
	s.mu.Lock()
	var ctx context.Context
	var deadline time.Time
	switch request.scope {
	case fixedOrdinary:
		if s.ordinary == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
		ctx, deadline, mode = s.ordinary.context, s.ordinary.deadline, readOrdinary
		if ctx == nil || deadline.IsZero() || !s.now().Before(deadline) {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	case fixedAssembly:
		ctx, deadline, mode = s.assemblyContext, s.assemblyDeadline, readAssembly
		if !s.assemblyLive || ctx == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	case fixedSpine:
		ctx, deadline, mode = s.spineContext, s.spineDeadline, readSpine
		if s.spineLease == nil || ctx == nil {
			s.mu.Unlock()
			return handle{}, failure(DiagnosticProtocol)
		}
	default:
		s.mu.Unlock()
		return handle{}, failure(DiagnosticProtocol)
	}
	s.mu.Unlock()
	var method, path string
	var body []byte
	var decode func([]byte) (*artifact, bool)
	switch request.purpose {
	case fixedRequestRoot:
		if request.kind != kindControlRef || request.ref == "" {
			return handle{}, failure(DiagnosticProtocol)
		}
		method = http.MethodGet
		path = "/repos/" + repository + "/git/ref/" + strings.TrimPrefix(request.ref, "refs/")
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRef(raw, request.ref, "commit")
			return &artifact{kind: kindControlRef, ref: value}, ok
		}
	case fixedRequestRaw:
		if request.kind != kindRawCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/commits/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRawCommit(raw, request.oid)
			return &artifact{kind: kindRawCommit, raw: value}, ok
		}
	case fixedRequestREST:
		if request.kind != kindRESTCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/commits/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeRESTCommit(raw, request.oid)
			return &artifact{kind: kindRESTCommit, rest: value}, ok
		}
	case fixedRequestGraphQL:
		if request.kind != kindGraphQLCommit || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path, body = http.MethodPost, "/graphql", stateV3GraphQLBody(request.oid)
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeGQLCommit(raw, request.oid)
			return &artifact{kind: kindGraphQLCommit, gql: value}, ok
		}
	case fixedRequestTree:
		if request.kind != kindTree || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/trees/"+request.oid+"?recursive=1"
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeTree(raw, request.oid)
			return &artifact{kind: kindTree, tree: value}, ok
		}
	case fixedRequestBlob:
		if request.kind != kindBlob || !validOID(request.oid) {
			return handle{}, failure(DiagnosticProtocol)
		}
		method, path = http.MethodGet, "/repos/"+repository+"/git/blobs/"+request.oid
		decode = func(raw []byte) (*artifact, bool) {
			value, ok := decodeBlob(raw, request.oid, maxResponseBytes)
			return &artifact{kind: kindBlob, blob: value}, ok
		}
	default:
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.executeRequest(mode, ctx, deadline, request.kind, method, path, body, decode)
}`

// TestNoGenericLeasedAuthoritySurface guards the closed continuation algebra.
// Active assembly and spine request settlement must take only fixedRequest;
// roots and successors may take only opaque handles, never wire authority.
func TestNoGenericLeasedAuthoritySurface(t *testing.T) {
	const collector = "collector.go"
	content, err := os.ReadFile(collector)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"executeAssemblyRequest", "executeSpineRequest", "executeAssemblyRaw", "executeSpineRaw",
		"WithSpineLease", "stateV3ReadLease", "context.WithValue", "assemblyLease,",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("%s restores forbidden authority surface %q", collector, forbidden)
		}
	}
	for _, name := range []string{"assembly.go", "operation.go"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"executeSpineRaw", "WithSpineLease", "context.WithValue", "assemblyLease,"} {
			if strings.Contains(string(content), forbidden) {
				t.Fatalf("%s restores forbidden authority surface %q", name, forbidden)
			}
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), collector, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "settleFixed" {
			continue
		}
		if function.Type.Params.NumFields() != 1 || exprName(function.Type.Params.List[0].Type) != "fixedRequest" {
			t.Fatalf("settleFixed must accept only fixedRequest, got %#v", function.Type.Params)
		}
		return
	}
	t.Fatal("settleFixed missing")
}

// TestNoGenericDocumentAuthoritySurface ensures only session-owned compact
// history can issue document capabilities or determine blob read routes. It
// intentionally scans every production Go source file so a generic authority
// cannot be restored under a new name or in a different source file.
func TestNoGenericDocumentAuthoritySurface(t *testing.T) {
	files, err := productionStrictcollectorFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDocumentAuthoritySurface(files); err != nil {
		t.Fatal(err)
	}
}

func productionStrictcollectorFiles(directory string) (map[string]*ast.File, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(directory, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		files[name] = file
	}
	return files, nil
}

type authorityFunction struct {
	filename    string
	declaration *ast.FuncDecl
}

// validateDocumentAuthoritySurface deliberately verifies settlement sinks, not
// an open-ended approximation of Go value flow. Every production settlement
// must construct a direct, keyed fixedRequest at the approved call site. This
// fails closed on helpers, locals, parameters, conversions, function values,
// and wrapper returns before they can become a new request authority path.
func validateDocumentAuthoritySurface(files map[string]*ast.File) error {
	if len(files) == 0 {
		return fmt.Errorf("no production strictcollector files")
	}
	functions := authorityFunctions(files)
	if err := validateDocumentAdmissionIssuers(functions, documentAdmissionAliases(files)); err != nil {
		return err
	}
	requestAliases := fixedRequestAliases(files)
	if err := validateFixedRequestConstruction(functions, requestAliases); err != nil {
		return err
	}
	if err := validateDirectSettlementPolicy(files, functions, requestAliases); err != nil {
		return err
	}
	if err := validateExecutionSinks(files, functions); err != nil {
		return err
	}
	if err := validateFinalTransportSinks(files, functions); err != nil {
		return err
	}
	return validateOrdinaryOperationAuthority(files, functions)
}

// validateDirectSettlementPolicy is deliberately narrower than a Go data-flow
// analysis. fixedRequest and settleFixed are closed implementation sinks: a
// request may be created only as the direct keyed argument of one audited
// settleFixed call, and settleFixed may be referenced only as that direct
// selector callee. This makes helper, local, parameter, method-value, and
// wrapper routing fail closed without attempting to infer arbitrary flows.
func validateDirectSettlementPolicy(files map[string]*ast.File, functions []*authorityFunction, aliases map[string]bool) error {
	if err := rejectGlobalFixedAuthorityEscapes(files, aliases); err != nil {
		return err
	}
	expected := map[string]directSettlementExpectation{
		"collector.go:ReadMinorStateRef":               {"session", "ReadMinorStateRef", []string{"context.Context", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindControlRef", "fixedRequestRoot", "ref", "gardenerrelease.StateV3MinorStateRef", true},
		"collector.go:ReadPatchStateRef":               {"session", "ReadPatchStateRef", []string{"context.Context", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindControlRef", "fixedRequestRoot", "ref", "gardenerrelease.StateV3PatchStateRef", true},
		"collector.go:ReadCoordinationRef":             {"session", "ReadCoordinationRef", []string{"context.Context", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindControlRef", "fixedRequestRoot", "ref", "gardenerrelease.StateV3CoordinationRef", true},
		"collector.go:ReadRawCommitForRef":             {"session", "ReadRawCommitForRef", []string{"context.Context", "handle", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindRawCommit", "fixedRequestRaw", "oid", "ref.SHA", true},
		"collector.go:ReadRawCommitParent":             {"session", "ReadRawCommitParent", []string{"context.Context", "handle", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindRawCommit", "fixedRequestRaw", "oid", "commit.Parents[0]", true},
		"collector.go:ReadRESTCommitForRawCommit":      {"session", "ReadRESTCommitForRawCommit", []string{"context.Context", "handle", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindRESTCommit", "fixedRequestREST", "oid", "commit.SHA", true},
		"collector.go:ReadGraphQLCommitForRawCommit":   {"session", "ReadGraphQLCommitForRawCommit", []string{"context.Context", "handle", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindGraphQLCommit", "fixedRequestGraphQL", "oid", "commit.SHA", true},
		"collector.go:ReadTreeForRawCommit":            {"session", "ReadTreeForRawCommit", []string{"context.Context", "handle", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindTree", "fixedRequestTree", "oid", "commit.Tree", true},
		"collector.go:ReadBlobForTreeEntry":            {"session", "ReadBlobForTreeEntry", []string{"context.Context", "handle", "int", "time.Time"}, []string{"handle", "Result"}, "fixedOrdinary", "kindBlob", "fixedRequestBlob", "oid", "entry.SHA", true},
		"assembly.go:readStateV3SpineRoot":             {"session", "readStateV3SpineRoot", nil, []string{"handle", "Result"}, "fixedSpine", "kindControlRef", "fixedRequestRoot", "ref", "ref", false},
		"assembly.go:readSpineRawForRef":               {"session", "readSpineRawForRef", []string{"handle"}, []string{"handle", "Result"}, "fixedSpine", "kindRawCommit", "fixedRequestRaw", "oid", "ref.SHA", false},
		"assembly.go:readSpineRawParent":               {"session", "readSpineRawParent", []string{"handle"}, []string{"handle", "Result"}, "fixedSpine", "kindRawCommit", "fixedRequestRaw", "oid", "commit.Parents[0]", false},
		"assembly.go:readSpineREST":                    {"session", "readSpineREST", []string{"handle"}, []string{"handle", "Result"}, "fixedSpine", "kindRESTCommit", "fixedRequestREST", "oid", "commit.SHA", false},
		"assembly.go:readSpineGraphQL":                 {"session", "readSpineGraphQL", []string{"handle"}, []string{"handle", "Result"}, "fixedSpine", "kindGraphQLCommit", "fixedRequestGraphQL", "oid", "commit.SHA", false},
		"assembly.go:readSpineTree":                    {"session", "readSpineTree", []string{"handle"}, []string{"handle", "Result"}, "fixedSpine", "kindTree", "fixedRequestTree", "oid", "commit.Tree", false},
		"collector.go:readAssemblyRoot":                {"session", "readAssemblyRoot", nil, []string{"handle", "Result"}, "fixedAssembly", "kindControlRef", "fixedRequestRoot", "ref", "ref", false},
		"collector.go:readAssemblyRawForRef":           {"session", "readAssemblyRawForRef", []string{"handle"}, []string{"handle", "Result"}, "fixedAssembly", "kindRawCommit", "fixedRequestRaw", "oid", "ref.SHA", false},
		"collector.go:readAssemblyRawParent":           {"session", "readAssemblyRawParent", []string{"handle"}, []string{"handle", "Result"}, "fixedAssembly", "kindRawCommit", "fixedRequestRaw", "oid", "commit.Parents[0]", false},
		"collector.go:readAssemblyREST":                {"session", "readAssemblyREST", []string{"handle"}, []string{"handle", "Result"}, "fixedAssembly", "kindRESTCommit", "fixedRequestREST", "oid", "commit.SHA", false},
		"collector.go:readAssemblyGraphQL":             {"session", "readAssemblyGraphQL", []string{"handle"}, []string{"handle", "Result"}, "fixedAssembly", "kindGraphQLCommit", "fixedRequestGraphQL", "oid", "commit.SHA", false},
		"collector.go:readAssemblyTree":                {"session", "readAssemblyTree", []string{"handle"}, []string{"handle", "Result"}, "fixedAssembly", "kindTree", "fixedRequestTree", "oid", "commit.Tree", false},
		"collector.go:readAssemblyBlob":                {"session", "readAssemblyBlob", []string{"handle", "int"}, []string{"handle", "Result"}, "fixedAssembly", "kindBlob", "fixedRequestBlob", "oid", "entry.SHA", false},
		"compact_admission.go:readAssemblyCompactBlob": {"session", "readAssemblyCompactBlob", []string{"uint64", "uint16", "int"}, []string{"handle", "Result"}, "fixedAssembly", "kindBlob", "fixedRequestBlob", "oid", "entry.oid", false},
	}
	seen := make(map[string]int)
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		if err := rejectFixedRequestEscapes(function, aliases, identity == "collector.go:settleFixed"); err != nil {
			return err
		}
		directCalls := directSettleFixedCalls(function.declaration)
		if err := rejectNonDirectSettleFixedReferences(function.declaration, directCalls); err != nil {
			return fmt.Errorf("%s: %w", identity, err)
		}
		for call := range directCalls {
			expectation, ok := expected[identity]
			if !ok {
				return fmt.Errorf("unapproved fixed settlement caller %s", identity)
			}
			if err := requireFunctionSignature(function.declaration, expectation.receiver, expectation.name, expectation.params, expectation.results); err != nil {
				return fmt.Errorf("fixed settlement signature %s: %w", identity, err)
			}
			if err := validatePinnedDirectFixedRequest(call, aliases, expectation); err != nil {
				return fmt.Errorf("fixed settlement %s: %w", identity, err)
			}
			seen[identity]++
		}
	}
	for identity := range expected {
		if seen[identity] != 1 {
			return fmt.Errorf("direct fixed settlements for %s = %d, want 1", identity, seen[identity])
		}
	}
	return nil
}

// validateExecutionSinks closes the generic transport execution layer beneath
// settleFixed. executeRequest may be reached only by the ordinary and fixed
// settlement paths; readOnce may be reached only by executeRequest. This keeps
// active read modes, request paths, bodies, and decoders unreachable from any
// unreviewed same-package caller.
// validateFinalTransportSinks pins the only request construction and transport
// dispatch calls. A future same-package helper cannot bypass fixed settlement
// by constructing an http request or calling the injected transport directly.
func validateFinalTransportSinks(files map[string]*ast.File, functions []*authorityFunction) error {
	for _, file := range files {
		httpNames, err := netHTTPImportNames(file)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			if err := rejectAlternativeHTTPAuthority(declaration, httpNames); err != nil {
				return err
			}
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				invalid := false
				ast.Inspect(declaration, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if ok && (selector.Sel.Name == "NewRequest" || selector.Sel.Name == "NewRequestWithContext" || selector.Sel.Name == "RoundTrip") {
						invalid = true
						return false
					}
					return true
				})
				if invalid {
					return fmt.Errorf("global request construction or transport dispatch is forbidden")
				}
				continue
			}
			requests := directSinkCalls(function, "NewRequestWithContext")
			rounds := directSinkCalls(function, "RoundTrip")
			if err := rejectNonDirectSinkReferences(function, "NewRequestWithContext", requests); err != nil {
				return err
			}
			if err := rejectNonDirectSinkReferences(function, "RoundTrip", rounds); err != nil {
				return err
			}
			invalidNewRequest := false
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "NewRequest" {
					invalidNewRequest = true
					return false
				}
				return true
			})
			if invalidNewRequest {
				return fmt.Errorf("http.NewRequest is forbidden")
			}
			for call := range requests {
				if function.Name.Name != "readOnce" || !directSelectorReceiver(call, "http") || len(call.Args) != 4 || expressionSource(call.Args[0]) != "ctx" || expressionSource(call.Args[1]) != "method" || formattedExpression(call.Args[2]) != "apiOrigin + path" || expressionSource(call.Args[3]) != "bytes.NewReader(body)" {
					return fmt.Errorf("unapproved request construction")
				}
			}
			for call := range rounds {
				if function.Name.Name != "readOnce" || !directSelectorReceiver(call, "s.transport") || !sameExpressionSources(call.Args, []string{"req"}) {
					return fmt.Errorf("unapproved transport dispatch")
				}
			}
			if function.Name.Name == "readOnce" {
				if len(requests) != 1 || len(rounds) != 1 {
					return fmt.Errorf("readOnce must construct and dispatch exactly one request")
				}
			} else if len(requests) != 0 || len(rounds) != 0 {
				return fmt.Errorf("unapproved request or transport sink")
			}
		}
	}
	return nil
}

// rejectAlternativeHTTPAuthority keeps the injected RoundTripper as the sole
// transport surface. Any alternate net/http request constructor, client, or
// dispatch primitive creates a new request-authority path outside readOnce.
// netHTTPImportNames resolves net/http package identifiers before checking
// request authority. Canonical http is retained for synthetic AST fixtures;
// production aliases are discovered from their actual imports and cannot hide
// a constructor, client, or dispatch call.
func netHTTPImportNames(file *ast.File) (map[string]bool, error) {
	names := map[string]bool{"http": true}
	for _, specification := range file.Imports {
		if strings.Trim(specification.Path.Value, `"`) != "net/http" {
			continue
		}
		if specification.Name == nil {
			names["http"] = true
			continue
		}
		switch specification.Name.Name {
		case ".", "_":
			return nil, fmt.Errorf("dot or blank net/http import is forbidden")
		default:
			names[specification.Name.Name] = true
		}
	}
	return names, nil
}

func isNetHTTPSelector(expression ast.Expr, names map[string]bool) bool {
	return names[exprName(expression)]
}

func netHTTPComposite(expression ast.Expr, names map[string]bool) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && isNetHTTPSelector(selector.X, names) && (selector.Sel.Name == "Request" || selector.Sel.Name == "Client" || selector.Sel.Name == "Transport")
}

func rejectAlternativeHTTPAuthority(node ast.Node, httpNames map[string]bool) error {
	// Do not try to infer arbitrary client value flow. This trusted package has
	// no legitimate use of these request-dispatch selector names outside the
	// response-header reads below, so rejecting their every spelling also closes
	// method values, type assertions, conversions, and local forwarding aliases.
	readOnce, isReadOnce := node.(*ast.FuncDecl)
	if isReadOnce && readOnce.Name.Name != "readOnce" {
		isReadOnce = false
	}
	approvedHeaderGets := auditedResponseHeaderGets(readOnce, isReadOnce)
	var invalid string
	ast.Inspect(node, func(current ast.Node) bool {
		if invalid != "" {
			return false
		}
		switch value := current.(type) {
		case *ast.CompositeLit:
			if netHTTPComposite(value.Type, httpNames) {
				invalid = "net/http request or client composite is forbidden"
			}
		case *ast.CallExpr:
			if identifier, ok := value.Fun.(*ast.Ident); ok && identifier.Name == "new" && len(value.Args) == 1 && netHTTPComposite(value.Args[0], httpNames) {
				invalid = "net/http request or client allocation is forbidden"
			}
		case *ast.SelectorExpr:
			if isNetHTTPSelector(value.X, httpNames) {
				switch value.Sel.Name {
				case "DefaultClient", "DefaultTransport":
					invalid = "net/http default client or transport is forbidden"
				case "NewRequest":
					invalid = "alternative net/http request construction is forbidden"
				case "NewRequestWithContext":
					if exprName(value.X) != "http" {
						invalid = "aliased net/http request construction is forbidden"
					}
				}
			}
			switch value.Sel.Name {
			case "Do", "Head", "Post", "PostForm":
				invalid = "alternative net/http request dispatch is forbidden"
			case "Get":
				if !approvedHeaderGets[value] {
					invalid = "alternative net/http request dispatch is forbidden"
				}
			}
		}
		return invalid == ""
	})
	if invalid != "" {
		return fmt.Errorf("%s", invalid)
	}
	if isReadOnce && len(approvedHeaderGets) != 5 {
		return fmt.Errorf("readOnce response header reads are not the audited fixed shape")
	}
	return nil
}

// auditedResponseHeaderGets allows only the five current response-header
// lookups, as direct calls with their fixed header names. In particular, it
// does not admit a response.Header.Get method value or another Get selector.
func auditedResponseHeaderGets(function *ast.FuncDecl, isReadOnce bool) map[*ast.SelectorExpr]bool {
	approved := make(map[*ast.SelectorExpr]bool)
	if !isReadOnce {
		return approved
	}
	expected := map[string]int{
		`"Content-Encoding"`:      2,
		`"Retry-After"`:           1,
		`"X-RateLimit-Remaining"`: 1,
		`"X-RateLimit-Reset"`:     1,
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Get" || expressionSource(selector.X) != "response.Header" {
			return true
		}
		header, ok := call.Args[0].(*ast.BasicLit)
		if !ok || header.Kind != token.STRING || expected[header.Value] == 0 {
			return true
		}
		expected[header.Value]--
		approved[selector] = true
		return true
	})
	return approved
}

// validateOrdinaryOperationAuthority keeps the deferred ordinary setup path
// inert until a separately reviewed production session factory exists. Rather
// than approximating arbitrary aliases and conversions, it bans every source
// spelling of the capability except the three audited implementations.
func validateOrdinaryOperationAuthority(files map[string]*ast.File, functions []*authorityFunction) error {
	const (
		installer  = "collector.go:installOrdinary"
		settlement = "collector.go:settleFixed"
		closer     = "collector.go:Close"
	)
	if err := validateOrdinaryOperationDeclaration(files); err != nil {
		return err
	}
	allowed := map[string]bool{installer: true, settlement: true, closer: true}
	selectorCounts := make(map[string]int)
	operationReferences := 0
	foundInstaller := false
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		if identity == installer {
			if foundInstaller || requireFunctionSignature(function.declaration, "session", "installOrdinary", []string{"context.Context", "time.Time"}, []string{"bool"}) != nil {
				return fmt.Errorf("ordinary installer has unexpected signature")
			}
			foundInstaller = true
			if formattedNode(function.declaration.Body) != `{
	if parent == nil || deadline.IsZero() || !s.now().Before(deadline) {
		return false
	}
	context, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.closed || s.ordinary != nil {
		s.mu.Unlock()
		cancel()
		return false
	}
	s.ordinary = &ordinaryOperation{context: context, deadline: deadline, cancel: cancel}
	s.mu.Unlock()
	return true
}` {
				return fmt.Errorf("ordinary installer body is not the audited installation path")
			}
		}
		if identity == settlement && formattedNode(function.declaration.Body) != auditedSettleFixedBody {
			return fmt.Errorf("ordinary settlement body is not the audited fixed path")
		}
		if identity == closer && formattedNode(function.declaration.Body) != `{
	s.mu.Lock()
	ordinary := s.ordinary
	s.ordinary = nil
	clear(s.artifacts)
	s.reservedReads = 0
	s.spineLease = nil
	s.spineContext = nil
	s.spineDeadline = time.Time{}
	s.spineRoot = 0
	s.assembly = nil
	s.assemblyLease = nil
	s.assemblyRole = 0
	s.assemblyContext = nil
	s.assemblyDeadline = time.Time{}
	s.assemblyReads = 0
	s.assemblyBytes = 0
	s.assemblyLive = false
	s.retainedBytes = 0
	s.closed = true
	s.mu.Unlock()
	if ordinary != nil {
		ordinary.cancel()
	}
}` {
			return fmt.Errorf("ordinary close body is not the audited detachment path")
		}
		var invalid string
		ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
			if invalid != "" {
				return false
			}
			switch value := node.(type) {
			case *ast.Ident:
				if value.Name == "ordinaryOperation" {
					operationReferences++
					if identity != installer {
						invalid = "ordinary operation construction or type use is unapproved"
					}
				}
			case *ast.SelectorExpr:
				switch value.Sel.Name {
				case "installOrdinary":
					invalid = "ordinary installer references are forbidden before a reviewed factory"
				case "ordinary":
					if !allowed[identity] || expressionSource(value) != "s.ordinary" {
						invalid = "ordinary operation reference is unapproved"
						return false
					}
					selectorCounts[identity]++
				}
			}
			return true
		})
		if invalid != "" {
			return fmt.Errorf("%s: %s", identity, invalid)
		}
	}
	if !foundInstaller {
		return fmt.Errorf("ordinary installer missing")
	}
	if selectorCounts[installer] != 2 || selectorCounts[settlement] != 3 || selectorCounts[closer] != 2 {
		return fmt.Errorf("ordinary operation references are not the audited installation, settlement, and close paths")
	}
	for filename, file := range files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if isFunction {
				if fieldListUsesOrdinaryOperation(function.Type.Params) || fieldListUsesOrdinaryOperation(function.Type.Results) {
					return fmt.Errorf("%s:%s: ordinary operation signature is forbidden", filename, function.Name.Name)
				}
				continue
			}
			var invalid bool
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.Ident:
					if node.Name == "ordinaryOperation" {
						operationReferences++
					}
				case *ast.SelectorExpr:
					if node.Sel.Name == "ordinary" || node.Sel.Name == "installOrdinary" {
						invalid = true
						return false
					}
				}
				return true
			})
			if invalid {
				return fmt.Errorf("global ordinary operation authority is forbidden")
			}
		}
	}
	// The sole legal spellings are the type declaration, session field, and
	// installer literal. This also rejects aliases and defined wrapper types.
	if operationReferences != 3 {
		return fmt.Errorf("ordinary operation type authority is not structurally closed")
	}
	return nil
}

func validateOrdinaryOperationDeclaration(files map[string]*ast.File) error {
	file := files["collector.go"]
	if file == nil {
		return fmt.Errorf("ordinary operation declarations must be in collector.go")
	}
	var session, operation *ast.StructType
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			switch typeSpec.Name.Name {
			case "session":
				session = structure
			case "ordinaryOperation":
				operation = structure
			}
		}
	}
	if session == nil || operation == nil || !hasExactOrdinarySessionField(session) || !hasExactOrdinaryOperationFields(operation) {
		return fmt.Errorf("ordinary operation declaration is not the audited fixed shape")
	}
	return nil
}

func hasExactOrdinarySessionField(structure *ast.StructType) bool {
	matches := 0
	for _, field := range structure.Fields.List {
		if len(field.Names) == 1 && field.Names[0].Name == "ordinary" {
			star, ok := field.Type.(*ast.StarExpr)
			if !ok || astTypeName(star.X) != "ordinaryOperation" {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

func hasExactOrdinaryOperationFields(structure *ast.StructType) bool {
	expected := []struct{ name, typ string }{
		{"context", "context.Context"},
		{"deadline", "time.Time"},
		{"cancel", "context.CancelFunc"},
	}
	if len(structure.Fields.List) != len(expected) {
		return false
	}
	for index, field := range structure.Fields.List {
		if len(field.Names) != 1 || field.Names[0].Name != expected[index].name || astTypeName(field.Type) != expected[index].typ {
			return false
		}
	}
	return true
}

func fieldListUsesOrdinaryOperation(fields *ast.FieldList) bool {
	for _, name := range fieldTypeNames(fields) {
		if name == "ordinaryOperation" {
			return true
		}
	}
	return false
}

func validateExecutionSinks(files map[string]*ast.File, functions []*authorityFunction) error {
	if err := rejectGlobalExecutionSinkReferences(files); err != nil {
		return err
	}
	expectedExecution := map[string]executionSinkExpectation{
		"collector.go:settleFixed": {"session", "settleFixed", []string{"fixedRequest"}, []string{"handle", "Result"}, []string{"mode", "ctx", "deadline", "request.kind", "method", "path", "body", "decode"}},
	}
	expectedReadOnce := executionSinkExpectation{
		receiver: "session",
		name:     "executeRequest",
		params:   []string{"readMode", "context.Context", "time.Time", "kind", "string", "string", "*ast.ArrayType", "*ast.FuncType"},
		results:  []string{"handle", "Result"},
		args:     []string{"requestContext", "k", "method", "path", "body", "attempt"},
	}
	seenExecution := make(map[string]int)
	seenReadOnce := 0
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		executionCalls := directSinkCalls(function.declaration, "executeRequest")
		if err := rejectNonDirectSinkReferences(function.declaration, "executeRequest", executionCalls); err != nil {
			return fmt.Errorf("%s: %w", identity, err)
		}
		for call := range executionCalls {
			if !directSelectorReceiver(call, "s") {
				return fmt.Errorf("executeRequest receiver is not s in %s", identity)
			}
			expectation, ok := expectedExecution[identity]
			if !ok {
				return fmt.Errorf("unapproved executeRequest caller %s", identity)
			}
			if err := requireFunctionSignature(function.declaration, expectation.receiver, expectation.name, expectation.params, expectation.results); err != nil {
				return fmt.Errorf("executeRequest caller signature %s: %w", identity, err)
			}
			if !sameExpressionSources(call.Args, expectation.args) {
				return fmt.Errorf("executeRequest arguments are not approved in %s", identity)
			}
			seenExecution[identity]++
		}

		readOnceCalls := directSinkCalls(function.declaration, "readOnce")
		if err := rejectNonDirectSinkReferences(function.declaration, "readOnce", readOnceCalls); err != nil {
			return fmt.Errorf("%s: %w", identity, err)
		}
		for call := range readOnceCalls {
			if !directSelectorReceiver(call, "s") {
				return fmt.Errorf("readOnce receiver is not s in %s", identity)
			}
			if identity != "collector.go:executeRequest" {
				return fmt.Errorf("unapproved readOnce caller %s", identity)
			}
			if err := requireFunctionSignature(function.declaration, expectedReadOnce.receiver, expectedReadOnce.name, expectedReadOnce.params, expectedReadOnce.results); err != nil {
				return fmt.Errorf("readOnce caller signature: %w", err)
			}
			if !sameExpressionSources(call.Args, expectedReadOnce.args) {
				return fmt.Errorf("readOnce arguments are not approved")
			}
			seenReadOnce++
		}
	}
	for identity := range expectedExecution {
		if seenExecution[identity] != 1 {
			return fmt.Errorf("executeRequest calls for %s = %d, want 1", identity, seenExecution[identity])
		}
	}
	if seenReadOnce != 1 {
		return fmt.Errorf("readOnce calls = %d, want 1", seenReadOnce)
	}
	return nil
}

func formattedExpression(expression ast.Expr) string {
	return formattedNode(expression)
}

func formattedNode(node ast.Node) string {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
		return ""
	}
	return buffer.String()
}

func directSelectorReceiver(call *ast.CallExpr, receiver string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && expressionSource(selector.X) == receiver
}

type executionSinkExpectation struct {
	receiver, name  string
	params, results []string
	args            []string
}

func rejectGlobalExecutionSinkReferences(files map[string]*ast.File) error {
	for _, file := range files {
		for _, declaration := range file.Decls {
			if _, isFunction := declaration.(*ast.FuncDecl); isFunction {
				continue
			}
			invalid := false
			ast.Inspect(declaration, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if ok && (selector.Sel.Name == "executeRequest" || selector.Sel.Name == "readOnce") {
					invalid = true
					return false
				}
				return true
			})
			if invalid {
				return fmt.Errorf("global execution sink reference is forbidden")
			}
		}
	}
	return nil
}

func directSinkCalls(function *ast.FuncDecl, name string) map[*ast.CallExpr]bool {
	calls := make(map[*ast.CallExpr]bool)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			calls[call] = true
		}
		return true
	})
	return calls
}

func rejectNonDirectSinkReferences(function *ast.FuncDecl, name string, direct map[*ast.CallExpr]bool) error {
	directSelectors := make(map[*ast.SelectorExpr]bool)
	for call := range direct {
		directSelectors[call.Fun.(*ast.SelectorExpr)] = true
	}
	invalid := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && !directSelectors[selector] {
			invalid = true
			return false
		}
		return true
	})
	if invalid {
		return fmt.Errorf("%s may only be a direct selector callee", name)
	}
	return nil
}

func sameExpressionSources(expressions []ast.Expr, expected []string) bool {
	if len(expressions) != len(expected) {
		return false
	}
	for index, expression := range expressions {
		if expressionSource(expression) != expected[index] {
			return false
		}
	}
	return true
}

func rejectGlobalFixedAuthorityEscapes(files map[string]*ast.File, aliases map[string]bool) error {
	for _, file := range files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range general.Specs {
				switch value := specification.(type) {
				case *ast.ValueSpec:
					if fieldListUsesAlias(&ast.FieldList{List: []*ast.Field{{Type: value.Type}}}, aliases) {
						return fmt.Errorf("global fixedRequest value is forbidden")
					}
					for _, expression := range value.Values {
						if referencesSettleFixed(expression) {
							return fmt.Errorf("global settleFixed reference is forbidden")
						}
					}
				case *ast.TypeSpec:
					if value.Name.Name != "fixedRequest" && aliases[astTypeName(value.Type)] {
						return fmt.Errorf("fixedRequest aliases are forbidden")
					}
				}
			}
		}
	}
	return nil
}

func referencesSettleFixed(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "settleFixed" {
			found = true
			return false
		}
		return true
	})
	return found
}

type directSettlementExpectation struct {
	receiver, name                string
	params, results               []string
	scope, kind, purpose, payload string
	payloadExpression             string
	ordinary                      bool
}

func directSettleFixedCalls(function *ast.FuncDecl) map[*ast.CallExpr]bool {
	calls := make(map[*ast.CallExpr]bool)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "settleFixed" {
			calls[call] = true
		}
		return true
	})
	return calls
}

func rejectNonDirectSettleFixedReferences(function *ast.FuncDecl, direct map[*ast.CallExpr]bool) error {
	directSelectors := make(map[*ast.SelectorExpr]bool)
	for call := range direct {
		directSelectors[call.Fun.(*ast.SelectorExpr)] = true
	}
	var invalid bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "settleFixed" && !directSelectors[selector] {
			invalid = true
			return false
		}
		return true
	})
	if invalid {
		return fmt.Errorf("settleFixed may only be a direct selector callee")
	}
	return nil
}

func rejectFixedRequestEscapes(function *authorityFunction, aliases map[string]bool, isSettlementImplementation bool) error {
	if isSettlementImplementation {
		if err := requireFunctionSignature(function.declaration, "session", "settleFixed", []string{"fixedRequest"}, []string{"handle", "Result"}); err != nil {
			return err
		}
		return nil
	}
	allowedLiterals := make(map[*ast.CompositeLit]bool)
	for call := range directSettleFixedCalls(function.declaration) {
		if len(call.Args) == 1 {
			if literal, ok := call.Args[0].(*ast.CompositeLit); ok {
				allowedLiterals[literal] = true
			}
		}
	}
	var invalid bool
	ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			if aliases[astTypeName(value.Type)] && !allowedLiterals[value] {
				invalid = true
			}
		case *ast.ValueSpec:
			if aliases[astTypeName(value.Type)] {
				invalid = true
			}
		case *ast.CallExpr:
			if (exprName(value.Fun) == "new" && len(value.Args) == 1 && aliases[astTypeName(value.Args[0])]) || aliases[exprName(value.Fun)] {
				invalid = true
			}
		case *ast.UnaryExpr:
			if value.Op == token.AND && isFixedRequestComposite(value.X, aliases) {
				invalid = true
			}
		}
		return !invalid
	})
	if fieldListUsesAlias(function.declaration.Type.Params, aliases) || fieldListUsesAlias(function.declaration.Type.Results, aliases) {
		invalid = true
	}
	if invalid {
		return fmt.Errorf("unapproved fixedRequest construction or escape %s:%s", function.filename, function.declaration.Name.Name)
	}
	return nil
}

func isFixedRequestComposite(expression ast.Expr, aliases map[string]bool) bool {
	literal, ok := expression.(*ast.CompositeLit)
	return ok && aliases[astTypeName(literal.Type)]
}

func fieldListUsesAlias(fields *ast.FieldList, aliases map[string]bool) bool {
	for _, name := range fieldTypeNames(fields) {
		if aliases[name] {
			return true
		}
	}
	return false
}

func validatePinnedDirectFixedRequest(call *ast.CallExpr, aliases map[string]bool, expectation directSettlementExpectation) error {
	if len(call.Args) != 1 {
		return fmt.Errorf("settleFixed arguments = %d, want 1", len(call.Args))
	}
	literal, ok := call.Args[0].(*ast.CompositeLit)
	if !ok || !aliases[astTypeName(literal.Type)] {
		return fmt.Errorf("settlement request must be a direct fixedRequest literal")
	}
	fields := make(map[string]ast.Expr)
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return fmt.Errorf("settlement request must be keyed")
		}
		name := exprName(field.Key)
		if name == "" || fields[name] != nil {
			return fmt.Errorf("invalid settlement request key")
		}
		fields[name] = field.Value
	}
	if len(fields) != 4 || exprName(fields["scope"]) != expectation.scope || exprName(fields["kind"]) != expectation.kind || exprName(fields["purpose"]) != expectation.purpose || fields[expectation.payload] == nil {
		return fmt.Errorf("settlement request shape is not approved")
	}
	other := "oid"
	if expectation.payload == "oid" {
		other = "ref"
	}
	if fields[other] != nil || expressionSource(fields[expectation.payload]) != expectation.payloadExpression {
		return fmt.Errorf("settlement payload source is not approved")
	}
	if fields["ctx"] != nil || fields["deadline"] != nil {
		return fmt.Errorf("fixed settlement must not select context or deadline")
	}
	return nil
}

func expressionSource(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := expressionSource(value.X)
		if prefix == "" {
			return ""
		}
		return prefix + "." + value.Sel.Name
	case *ast.IndexExpr:
		return expressionSource(value.X) + "[" + expressionSource(value.Index) + "]"
	case *ast.CallExpr:
		if len(value.Args) == 1 {
			return expressionSource(value.Fun) + "(" + expressionSource(value.Args[0]) + ")"
		}
		return ""
	case *ast.BasicLit:
		return value.Value
	default:
		return ""
	}
}

func authorityFunctions(files map[string]*ast.File) []*authorityFunction {
	var functions []*authorityFunction
	for _, filename := range sortedASTFiles(files) {
		for _, declaration := range files[filename].Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			functions = append(functions, &authorityFunction{filename: filename, declaration: function})
		}
	}
	return functions
}

func fixedRequestAliases(files map[string]*ast.File) map[string]bool {
	aliases := map[string]bool{"fixedRequest": true}
	changed := true
	for changed {
		changed = false
		for _, file := range files {
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, specification := range general.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if ok && aliases[astTypeName(typeSpec.Type)] && !aliases[typeSpec.Name.Name] {
						aliases[typeSpec.Name.Name] = true
						changed = true
					}
				}
			}
		}
	}
	return aliases
}

func documentAdmissionAliases(files map[string]*ast.File) map[string]bool {
	aliases := map[string]bool{"stateV3DocumentAdmission": true}
	changed := true
	for changed {
		changed = false
		for _, file := range files {
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, specification := range general.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if ok && aliases[astTypeName(typeSpec.Type)] && !aliases[typeSpec.Name.Name] {
						aliases[typeSpec.Name.Name] = true
						changed = true
					}
				}
			}
		}
	}
	return aliases
}

func validateDocumentAdmissionIssuers(functions []*authorityFunction, aliases map[string]bool) error {
	const issuer = "admission.go:admitCompact"
	issuerFound := false
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		if functionMentionsDocumentAdmission(function.declaration, aliases) && identity != issuer && !isApprovedAdmissionConsumer(function) {
			return fmt.Errorf("unapproved document admission construction or forwarding path %s", identity)
		}
		if identity == issuer {
			issuerFound = true
			if err := requireFunctionSignature(function.declaration, "stateV3LaneHistoryAdmission", "admitCompact", []string{"uint16", "int"}, []string{"stateV3DocumentAdmission", "bool"}); err != nil {
				return fmt.Errorf("document token issuer %s: %w", identity, err)
			}
		}
		if err := validateAdmissionAssignments(function, aliases); err != nil {
			return err
		}
		if callsNamed(function.declaration, "admitCompact") && identity != "compact_admission.go:collectStateV3LaneDocuments" {
			return fmt.Errorf("unapproved document admission forwarding call %s", identity)
		}
	}
	if !issuerFound {
		return fmt.Errorf("production document token issuer missing %s", issuer)
	}
	return nil
}

func isApprovedAdmissionConsumer(function *authorityFunction) bool {
	name := function.declaration.Name.Name
	return function.filename == "admission.go" && (name == "permits" || name == "close")
}

func functionMentionsDocumentAdmission(function *ast.FuncDecl, aliases map[string]bool) bool {
	if fieldListUsesDocumentAdmission(function.Type.Params, aliases) || fieldListUsesDocumentAdmission(function.Type.Results, aliases) || aliases[receiverTypeName(function)] {
		return true
	}
	mentioned := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			if aliases[astTypeName(value.Type)] {
				mentioned = true
			}
		case *ast.CallExpr:
			if exprName(value.Fun) == "new" && len(value.Args) == 1 && aliases[astTypeName(value.Args[0])] {
				mentioned = true
			}
		case *ast.ValueSpec:
			if aliases[astTypeName(value.Type)] {
				mentioned = true
			}
		}
		return !mentioned
	})
	return mentioned
}

func validateAdmissionAssignments(function *authorityFunction, aliases map[string]bool) error {
	assignments := 0
	var invalid bool
	ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, left := range assignment.Lhs {
			if assignmentTargetsAdmission(left) {
				assignments++
				if !isExactApprovedAdmissionAssignment(function, assignment, index, aliases) {
					invalid = true
					return false
				}
			}
		}
		return true
	})
	if assignments != 0 && (assignments != 1 || !hasApprovedAdmissionFlow(function.declaration)) {
		invalid = true
	}
	if invalid {
		return fmt.Errorf("unapproved document admission assignment %s:%s", function.filename, function.declaration.Name.Name)
	}
	return nil
}

// hasApprovedAdmissionFlow pins the sole capability attachment to the exact
// compact-history issuer and checked failure branch in the collection loop.
func hasApprovedAdmissionFlow(function *ast.FuncDecl) bool {
	var issued, guarded bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if len(value.Lhs) == 2 && len(value.Rhs) == 1 && exprName(value.Lhs[0]) == "token" && exprName(value.Lhs[1]) == "admitted" {
				call, ok := value.Rhs[0].(*ast.CallExpr)
				selector, selectorOK := call.Fun.(*ast.SelectorExpr)
				issued = ok && selectorOK && exprName(selector.X) == "admission" && selector.Sel.Name == "admitCompact" && len(call.Args) == 2 && expressionSource(call.Args[0]) == "uint16(child)" && exprName(call.Args[1]) == "document"
			}
		case *ast.IfStmt:
			guarded = guarded || expressionNegatesIdentifier(value.Cond, "admitted")
		}
		return true
	})
	return issued && guarded
}

func expressionNegatesIdentifier(expression ast.Expr, name string) bool {
	switch value := expression.(type) {
	case *ast.UnaryExpr:
		return value.Op == token.NOT && exprName(value.X) == name
	case *ast.BinaryExpr:
		return expressionNegatesIdentifier(value.X, name) || expressionNegatesIdentifier(value.Y, name)
	default:
		return false
	}
}

func callsNamed(function *ast.FuncDecl, name string) bool {
	called := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && calledFunctionName(call.Fun) == name {
			called = true
			return false
		}
		return true
	})
	return called
}

func assignmentTargetsAdmission(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return assignmentTargetsAdmission(value.X)
	case *ast.StarExpr:
		return assignmentTargetsAdmission(value.X)
	case *ast.UnaryExpr:
		return value.Op == token.AND && assignmentTargetsAdmission(value.X)
	case *ast.SelectorExpr:
		return value.Sel.Name == "admission" || assignmentTargetsAdmission(value.X)
	default:
		return false
	}
}

func isExactApprovedAdmissionAssignment(function *authorityFunction, assignment *ast.AssignStmt, index int, aliases map[string]bool) bool {
	if function.filename != "compact_admission.go" || function.declaration.Name.Name != "collectStateV3LaneDocuments" || index != 0 || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	left, ok := assignment.Lhs[0].(*ast.SelectorExpr)
	if !ok || left.Sel.Name != "admission" || exprName(left.X) != "entry" || exprName(assignment.Rhs[0]) != "token" {
		return false
	}
	return true
}

func validateFixedRequestConstruction(functions []*authorityFunction, aliases map[string]bool) error {
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		var constructs, settles bool
		ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CompositeLit:
				if aliases[astTypeName(value.Type)] {
					constructs = true
				}
			case *ast.CallExpr:
				if calledFunctionName(value.Fun) == "settleFixed" {
					settles = true
				}
			case *ast.SelectorExpr:
				if knownBlobReaderName(value.Sel.Name) && !approvedBlobReaderIdentity(identity) {
					constructs = true
				}
			}
			return true
		})
		if constructs && !settles {
			return fmt.Errorf("unapproved fixed request construction or blob reader forwarding %s", identity)
		}
	}
	return nil
}

func approvedBlobReaderIdentity(identity string) bool {
	return identity == "collector.go:ReadBlobForTreeEntry" || identity == "collector.go:readAssemblyBlob" || identity == "compact_admission.go:readAssemblyCompactBlob" || identity == "compact_admission.go:loadCompactSnapshot"
}

func knownBlobReaderName(name string) bool {
	return name == "ReadBlobForTreeEntry" || name == "readAssemblyBlob" || name == "readAssemblyCompactBlob"
}

func validateFixedSettlements(functions []*authorityFunction, aliases map[string]bool) error {
	expected := map[string]fixedSettlementExpectation{
		"assembly.go:readStateV3SpineRoot":             {"fixedSpine", "kindControlRef", "fixedRequestRoot"},
		"assembly.go:readSpineRawForRef":               {"fixedSpine", "kindRawCommit", "fixedRequestRaw"},
		"assembly.go:readSpineRawParent":               {"fixedSpine", "kindRawCommit", "fixedRequestRaw"},
		"assembly.go:readSpineREST":                    {"fixedSpine", "kindRESTCommit", "fixedRequestREST"},
		"assembly.go:readSpineGraphQL":                 {"fixedSpine", "kindGraphQLCommit", "fixedRequestGraphQL"},
		"assembly.go:readSpineTree":                    {"fixedSpine", "kindTree", "fixedRequestTree"},
		"collector.go:readAssemblyRoot":                {"fixedAssembly", "kindControlRef", "fixedRequestRoot"},
		"collector.go:readAssemblyRawForRef":           {"fixedAssembly", "kindRawCommit", "fixedRequestRaw"},
		"collector.go:readAssemblyRawParent":           {"fixedAssembly", "kindRawCommit", "fixedRequestRaw"},
		"collector.go:readAssemblyREST":                {"fixedAssembly", "kindRESTCommit", "fixedRequestREST"},
		"collector.go:readAssemblyGraphQL":             {"fixedAssembly", "kindGraphQLCommit", "fixedRequestGraphQL"},
		"collector.go:readAssemblyTree":                {"fixedAssembly", "kindTree", "fixedRequestTree"},
		"collector.go:readAssemblyBlob":                {"fixedAssembly", "kindBlob", "fixedRequestBlob"},
		"compact_admission.go:readAssemblyCompactBlob": {"fixedAssembly", "kindBlob", "fixedRequestBlob"},
	}
	seen := make(map[string]int)
	for _, function := range functions {
		identity := function.filename + ":" + function.declaration.Name.Name
		var calls []*ast.CallExpr
		ast.Inspect(function.declaration.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && calledFunctionName(call.Fun) == "settleFixed" {
				calls = append(calls, call)
			}
			return true
		})
		for _, call := range calls {
			expectation, ok := expected[identity]
			if !ok {
				return fmt.Errorf("unapproved fixed settlement caller %s", identity)
			}
			if err := validateDirectFixedRequest(call, aliases, expectation); err != nil {
				return fmt.Errorf("fixed settlement %s: %w", identity, err)
			}
			seen[identity]++
		}
	}
	for identity := range expected {
		if seen[identity] != 1 {
			return fmt.Errorf("fixed settlements for %s = %d, want 1", identity, seen[identity])
		}
	}
	return nil
}

type fixedSettlementExpectation struct{ scope, kind, purpose string }

func validateDirectFixedRequest(call *ast.CallExpr, aliases map[string]bool, expectation fixedSettlementExpectation) error {
	if len(call.Args) != 1 {
		return fmt.Errorf("settleFixed arguments = %d, want 1", len(call.Args))
	}
	literal, ok := call.Args[0].(*ast.CompositeLit)
	if !ok || !aliases[astTypeName(literal.Type)] {
		return fmt.Errorf("settlement request must be direct fixedRequest literal")
	}
	fields := make(map[string]ast.Expr)
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return fmt.Errorf("settlement request must be keyed")
		}
		name := exprName(field.Key)
		if name == "" || fields[name] != nil {
			return fmt.Errorf("invalid settlement request key")
		}
		fields[name] = field.Value
	}
	if len(fields) != 4 || exprName(fields["scope"]) != expectation.scope || exprName(fields["kind"]) != expectation.kind || exprName(fields["purpose"]) != expectation.purpose {
		return fmt.Errorf("settlement request shape is not approved")
	}
	if expectation.purpose == "fixedRequestRoot" {
		if fields["ref"] == nil || fields["oid"] != nil {
			return fmt.Errorf("root settlement must contain only ref payload")
		}
	} else if fields["oid"] == nil || fields["ref"] != nil {
		return fmt.Errorf("object settlement must contain only oid payload")
	}
	return nil
}

func calledFunctionName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	default:
		return ""
	}
}

func fieldListUsesDocumentAdmission(fields *ast.FieldList, aliases map[string]bool) bool {
	for _, name := range fieldTypeNames(fields) {
		if aliases[name] {
			return true
		}
	}
	return false
}

func fieldTypeNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var types []string
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			types = append(types, astTypeName(field.Type))
		}
	}
	return types
}

func receiverTypeName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return ""
	}
	return astTypeName(function.Recv.List[0].Type)
}

func requireFunctionSignature(function *ast.FuncDecl, receiver, name string, params, results []string) error {
	if function.Name.Name != name || receiverTypeName(function) != receiver || !sameTypeNames(fieldTypeNames(function.Type.Params), params) || !sameTypeNames(fieldTypeNames(function.Type.Results), results) {
		return fmt.Errorf("unexpected signature")
	}
	return nil
}

func sortedASTFiles(files map[string]*ast.File) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func astTypeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return astTypeName(expression.X)
	case *ast.SelectorExpr:
		return astTypeName(expression.X) + "." + expression.Sel.Name
	default:
		return fmt.Sprintf("%T", expression)
	}
}

func sameTypeNames(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func TestDirectSettlementPolicyRejectsIndirectAuthority(t *testing.T) {
	for name, source := range map[string]string{
		"settle method value and field request": `package strictcollector
			func (s *session) route(oid string) (handle, Result) {
				var request fixedRequest
				request.scope = fixedAssembly; request.kind = kindBlob; request.purpose = fixedRequestBlob; request.oid = oid
				settle := s.settleFixed; return settle(request)
			}`,
		"settle method expression": `package strictcollector
			var indirectSettle = (*session).settleFixed
			func route(s *session, request fixedRequest) (handle, Result) { return indirectSettle(s, request) }`,
		"fixed request parameter": `package strictcollector
			func route(request fixedRequest) { _ = request }`,
		"settle method value only": `package strictcollector
			func (s *session) route() { _ = s.settleFixed }`,
	} {
		t.Run(name, func(t *testing.T) {
			files, err := productionStrictcollectorFiles(".")
			if err != nil {
				t.Fatal(err)
			}
			files["zz_bypass.go"] = parseAuthorityFixture(t, source)
			if err := validateDocumentAuthoritySurface(files); err == nil {
				t.Fatal("indirect fixed settlement authority accepted")
			}
		})
	}

	t.Run("widened approved blob reader", func(t *testing.T) {
		files, err := productionStrictcollectorFiles(".")
		if err != nil {
			t.Fatal(err)
		}
		function := authorityFunctionByIdentity(t, files, "collector.go", "readAssemblyBlob")
		function.Type.Params.List = append(function.Type.Params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("oid")}, Type: ast.NewIdent("string")})
		if err := validateDocumentAuthoritySurface(files); err == nil {
			t.Fatal("widened approved blob reader accepted")
		}
	})

	t.Run("fake admission assignment", func(t *testing.T) {
		files, err := productionStrictcollectorFiles(".")
		if err != nil {
			t.Fatal(err)
		}
		function := authorityFunctionByIdentity(t, files, "compact_admission.go", "collectStateV3LaneDocuments")
		function.Body.List = append(function.Body.List, &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("entry"), Sel: ast.NewIdent("admission")}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{ast.NewIdent("token")},
		})
		if err := validateDocumentAuthoritySurface(files); err == nil {
			t.Fatal("forged admission assignment accepted")
		}
	})
}

func authorityFunctionByIdentity(t *testing.T, files map[string]*ast.File, filename, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range files[filename].Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("function %s:%s missing", filename, name)
	return nil
}

func TestExecutionSinkPolicyRejectsIndirectAuthority(t *testing.T) {
	for name, source := range map[string]string{
		"direct active executeRequest": `package strictcollector
			func (s *session) route(ctx context.Context, deadline time.Time, path string, body []byte, decode func([]byte) (*artifact, bool)) (handle, Result) {
				return s.executeRequest(readAssembly, ctx, deadline, kindBlob, http.MethodGet, path, body, decode)
			}`,
		"executeRequest forwarding wrapper": `package strictcollector
			func (s *session) route(ctx context.Context, deadline time.Time, path string, body []byte, decode func([]byte) (*artifact, bool)) (handle, Result) {
				return s.executeRequest(readOrdinary, ctx, deadline, kindBlob, http.MethodGet, path, body, decode)
			}`,
		"executeRequest method value": `package strictcollector
			func (s *session) route() { _ = s.executeRequest }`,
		"executeRequest method expression": `package strictcollector
			var run = (*session).executeRequest`,
		"direct readOnce": `package strictcollector
			func (s *session) route(ctx context.Context, path string) { _, _, _, _ = s.readOnce(ctx, kindBlob, http.MethodGet, path, nil, 1) }`,
		"readOnce method value": `package strictcollector
			func (s *session) route() { _ = s.readOnce }`,
		"readOnce method expression": `package strictcollector
			var once = (*session).readOnce`,
		"direct request construction": `package strictcollector
			func route(ctx context.Context, method, path string) { _, _ = http.NewRequestWithContext(ctx, method, path, nil) }`,
		"request constructor method value": `package strictcollector
			func route() { _ = http.NewRequestWithContext }`,
		"direct transport dispatch": `package strictcollector
			func (s *session) route(req *http.Request) { _, _ = s.transport.RoundTrip(req) }`,
		"transport method value": `package strictcollector
			func (s *session) route() { _ = s.transport.RoundTrip }`,
		"http get": `package strictcollector
			func route(path string) { _, _ = http.Get(apiOrigin + path) }`,
		"http client dispatch": `package strictcollector
			func route(request *http.Request) { _, _ = (&http.Client{}).Do(request) }`,
		"http default client dispatch": `package strictcollector
			func route(request *http.Request) { _, _ = http.DefaultClient.Do(request) }`,
		"http request composite": `package strictcollector
			func route() { _ = http.Request{} }`,
		"http client allocation": `package strictcollector
			func route() { _ = new(http.Client) }`,
		"aliased http get": `package strictcollector
			import ghhttp "net/http"
			func route(url string) { _, _ = ghhttp.Get(url) }`,
		"aliased http get method value": `package strictcollector
			import ghhttp "net/http"
			var dispatch = ghhttp.Get
			func route(url string) { _, _ = dispatch(url) }`,
		"aliased http client type alias": `package strictcollector
			import ghhttp "net/http"
			type client = ghhttp.Client
			func route(c *client, url string) { _, _ = c.Get(url) }`,
		"local http client type alias": `package strictcollector
			import ghhttp "net/http"
			func route(url string) { type client = ghhttp.Client; var c *client; _, _ = c.Get(url) }`,
		"aliased http client head": `package strictcollector
			import ghhttp "net/http"
			func route(client *ghhttp.Client, url string) { _, _ = client.Head(url) }`,
		"aliased http client post": `package strictcollector
			import ghhttp "net/http"
			func route(client *ghhttp.Client, url string) { _, _ = client.Post(url, "", nil) }`,
		"aliased http client post form": `package strictcollector
			import ghhttp "net/http"
			func route(client *ghhttp.Client, url string) { _, _ = client.PostForm(url, nil) }`,
		"aliased http client method value": `package strictcollector
			import ghhttp "net/http"
			func route(client *ghhttp.Client) { _ = client.Get }`,
		"aliased http client method expression": `package strictcollector
			import ghhttp "net/http"
			func route() { _ = (*ghhttp.Client).Do }`,
		"forwarded http client": `package strictcollector
			import ghhttp "net/http"
			func route(client *ghhttp.Client, url string) { forwarded := client; _, _ = forwarded.Get(url) }`,
		"asserted http client": `package strictcollector
			import ghhttp "net/http"
			func route(url string) { var raw ghhttp.Client; var boxed any = &raw; client := boxed.(*ghhttp.Client); _, _ = client.Get(url) }`,
		"converted http client": `package strictcollector
			import ghhttp "net/http"
			type client = ghhttp.Client
			func route(url string) { var raw ghhttp.Client; var boxed any = &raw; converted := (*client)(boxed.(*ghhttp.Client)); _, _ = converted.Get(url) }`,
	} {
		t.Run(name, func(t *testing.T) {
			files, err := productionStrictcollectorFiles(".")
			if err != nil {
				t.Fatal(err)
			}
			files["zz_execution_bypass.go"] = parseAuthorityFixture(t, source)
			if err := validateDocumentAuthoritySurface(files); err == nil {
				t.Fatal("indirect execution authority accepted")
			}
		})
	}

	for _, sink := range []struct {
		name, caller, target string
	}{
		{"executeRequest", "settleFixed", "executeRequest"},
		{"readOnce", "executeRequest", "readOnce"},
	} {
		t.Run(sink.name+" receiver", func(t *testing.T) {
			files, err := productionStrictcollectorFiles(".")
			if err != nil {
				t.Fatal(err)
			}
			function := authorityFunctionByIdentity(t, files, "collector.go", sink.caller)
			call := onlyDirectSinkCall(t, function, sink.target)
			call.Fun.(*ast.SelectorExpr).X = ast.NewIdent("other")
			if err := validateDocumentAuthoritySurface(files); err == nil {
				t.Fatalf("%s receiver other accepted", sink.target)
			}
		})
	}
}

func TestOrdinaryOperationAuthorityRejectsSyntheticSeams(t *testing.T) {
	for name, source := range map[string]string{
		"ordinary direct assignment": `package strictcollector
			func (s *session) route() { s.ordinary = &ordinaryOperation{} }`,
		"ordinary direct reference": `package strictcollector
			func (s *session) route() { _ = s.ordinary }`,
		"ordinary installer call": `package strictcollector
			func (s *session) route(ctx context.Context, deadline time.Time) { _ = s.installOrdinary(ctx, deadline) }`,
		"ordinary installer method value": `package strictcollector
			func (s *session) route(ctx context.Context, deadline time.Time) { installer := s.installOrdinary; _ = installer(ctx, deadline) }`,
		"ordinary installer method expression": `package strictcollector
			func (s *session) route(ctx context.Context, deadline time.Time) { installer := (*session).installOrdinary; _ = installer(s, ctx, deadline) }`,
		"ordinary installer retained in container": `package strictcollector
			func (s *session) route() any { return []any{s.installOrdinary} }`,
		"ordinary global installer method expression": `package strictcollector
			var installer = (*session).installOrdinary`,
		"ordinary installer returned": `package strictcollector
			func (s *session) route() func(context.Context, time.Time) bool { return s.installOrdinary }`,
		"ordinary converted session receiver": `package strictcollector
			type alternateSession session
			func route(s *session) { converted := (*alternateSession)(s); _ = converted.ordinary }`,
		"ordinary operation field mutation": `package strictcollector
			func (s *session) route(ctx context.Context) { s.ordinary.context = ctx }`,
		"ordinary global construction": `package strictcollector
			var ordinary = ordinaryOperation{}`,
	} {
		t.Run(name, func(t *testing.T) {
			files, err := productionStrictcollectorFiles(".")
			if err != nil {
				t.Fatal(err)
			}
			files["zz_ordinary_bypass.go"] = parseAuthorityFixture(t, source)
			if err := validateDocumentAuthoritySurface(files); err == nil {
				t.Fatal("ordinary operation authority accepted")
			}
		})
	}

	t.Run("settlement ordinary mutation", func(t *testing.T) {
		files, err := productionStrictcollectorFiles(".")
		if err != nil {
			t.Fatal(err)
		}
		mutation := parseAuthorityFixture(t, `package strictcollector
			func route(s *session) { s.ordinary = nil }`)
		mutator := authorityFunctionByIdentity(t, map[string]*ast.File{"mutation.go": mutation}, "mutation.go", "route")
		settlement := authorityFunctionByIdentity(t, files, "collector.go", "settleFixed")
		settlement.Body.List = append(settlement.Body.List, mutator.Body.List...)
		if err := validateDocumentAuthoritySurface(files); err == nil {
			t.Fatal("ordinary mutation in settlement accepted")
		}
	})
}

func onlyDirectSinkCall(t *testing.T, function *ast.FuncDecl, sink string) *ast.CallExpr {
	t.Helper()
	calls := directSinkCalls(function, sink)
	if len(calls) != 1 {
		t.Fatalf("%s direct calls = %d, want 1", sink, len(calls))
	}
	for call := range calls {
		return call
	}
	return nil
}

func TestDocumentAuthoritySurfaceRejectsSyntheticSeams(t *testing.T) {
	for name, sources := range map[string]map[string]string{
		"generic token issuer": {
			"issuer.go": `package strictcollector
				func (a *stateV3LaneHistoryAdmission) mint(entry stateV3ApprovedDocumentEntry) (stateV3DocumentAdmission, bool) { return stateV3DocumentAdmission{}, false }`,
		},
		"token composite assignment": {
			"assignment.go": `package strictcollector
				func poison(entry *stateV3ApprovedDocumentEntry) { entry.admission = stateV3DocumentAdmission{} }`,
		},
		"nested token field assignment": {
			"assignment.go": `package strictcollector
				func poison(entry *stateV3ApprovedDocumentEntry) { entry.admission.owner = nil }`,
		},
		"token alias wrapper in separate source": {
			"alias.go": `package strictcollector
				type permit = stateV3DocumentAdmission`,
			"wrapper.go": `package strictcollector
				func (a *stateV3LaneHistoryAdmission) issue(ordinal uint16, document int) (permit, bool) { return a.admitCompact(ordinal, document) }`,
		},
		"innocuous assembly blob wrapper": {
			"wrapper.go": `package strictcollector
				func (s *session) route(prior handle, index int) (handle, Result) { return s.readAssemblyBlob(prior, index) }`,
		},
		"innocuous compact blob wrapper": {
			"wrapper.go": `package strictcollector
				func (s *session) route(generation uint64, ordinal uint16, document int) (handle, Result) { return s.readAssemblyCompactBlob(generation, ordinal, document) }`,
		},
		"local-result assembly blob wrapper": {
			"wrapper.go": `package strictcollector
				func (s *session) route(prior handle, index int) (handle, Result) { blob, result := s.readAssemblyBlob(prior, index); return blob, result }`,
		},
		"split blob request constructor and settler": {
			"builder.go": `package strictcollector
				func build(oid string) fixedRequest { return fixedRequest{purpose: fixedRequestBlob, oid: oid} }`,
			"settler.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(build(oid)) }`,
		},
		"aliased split blob request constructor": {
			"alias.go": `package strictcollector
				type request = fixedRequest`,
			"builder.go": `package strictcollector
				func build(oid string) request { return request{purpose: fixedRequestBlob, oid: oid} }`,
			"settler.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(build(oid)) }`,
		},
		"unkeyed blob request constructor": {
			"route.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(fixedRequest{fixedAssembly, kindBlob, fixedRequestBlob, oid, ""}) }`,
		},
		"const purpose split in separate source": {
			"purpose.go": `package strictcollector
				const blobPurpose = fixedRequestBlob`,
			"builder.go": `package strictcollector
				func build(oid string) fixedRequest { purpose := blobPurpose; return fixedRequest{purpose: purpose, oid: oid} }`,
			"settler.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(build(oid)) }`,
		},
		"helper purpose split in separate source": {
			"purpose.go": `package strictcollector
				func blobPurpose() fixedRequestPurpose { return fixedRequestBlob }`,
			"builder.go": `package strictcollector
				func build(oid string) fixedRequest { return fixedRequest{purpose: blobPurpose(), oid: oid} }`,
			"settler.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(build(oid)) }`,
		},
		"local result wrapper settlement": {
			"builder.go": `package strictcollector
				func build(oid string) fixedRequest { return fixedRequest{purpose: fixedRequestBlob, oid: oid} }`,
			"settler.go": `package strictcollector
				type routedBlob struct { handle handle; result Result }
				func (s *session) route(oid string) routedBlob { request := build(oid); handle, result := s.settleFixed(request); return routedBlob{handle: handle, result: result} }`,
		},
		"assembly blob method value": {
			"wrapper.go": `package strictcollector
				func (s *session) route(prior handle, index int) (handle, Result) { reader := s.readAssemblyBlob; return reader(prior, index) }`,
		},
		"compact blob method value": {
			"wrapper.go": `package strictcollector
				func (s *session) route(generation uint64, ordinal uint16, document int) (handle, Result) { reader := s.readAssemblyCompactBlob; return reader(generation, ordinal, document) }`,
		},
		"new token": {
			"token.go": `package strictcollector
				func poison(entry *stateV3ApprovedDocumentEntry) { token := new(stateV3DocumentAdmission); entry.admission = *token }`,
		},
		"aliased new token": {
			"alias.go": `package strictcollector
				type permit = stateV3DocumentAdmission`,
			"token.go": `package strictcollector
				func poison(entry *stateV3ApprovedDocumentEntry) { token := new(permit); entry.admission = *token }`,
		},
		"forwarded token": {
			"token.go": `package strictcollector
				func poison(entry *stateV3ApprovedDocumentEntry) { token, _ := (&stateV3LaneHistoryAdmission{}).admitCompact(0, 0); box := struct { token stateV3DocumentAdmission }{token: token}; entry.admission = box.token }`,
		},
		"pointer forwarded token": {
			"token.go": `package strictcollector
				func poison(a *stateV3LaneHistoryAdmission, entry *stateV3ApprovedDocumentEntry) { token, _ := a.admitCompact(0, 0); target := &entry.admission; *target = token }`,
		},
		"parameterized builder settlement": {
			"route.go": `package strictcollector
				func build(oid string, purpose fixedRequestPurpose) fixedRequest { return fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: purpose, oid: oid} }
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(build(oid, fixedRequestBlob)) }`,
		},
		"converted purpose settlement": {
			"route.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: fixedRequestPurpose(fixedRequestBlob), oid: oid}) }`,
		},
		"function literal purpose settlement": {
			"route.go": `package strictcollector
				func (s *session) route(oid string) (handle, Result) { return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: func() fixedRequestPurpose { return fixedRequestBlob }(), oid: oid}) }`,
		},
		"builder method value settlement": {
			"route.go": `package strictcollector
				func build(oid string) fixedRequest { return fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: fixedRequestBlob, oid: oid} }
				func (s *session) route(oid string) (handle, Result) { builder := build; return s.settleFixed(builder(oid)) }`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := documentAuthorityFixture(t)
			for filename, source := range sources {
				files[filename] = parseAuthorityFixture(t, source)
			}
			if err := validateDocumentAuthoritySurface(files); err == nil {
				t.Fatal("generic authority surface accepted")
			}
		})
	}
}

func documentAuthorityFixture(t *testing.T) map[string]*ast.File {
	t.Helper()
	return map[string]*ast.File{
		"admission.go": parseAuthorityFixture(t, `package strictcollector
			func (a *stateV3LaneHistoryAdmission) admitCompact(ordinal uint16, document int) (stateV3DocumentAdmission, bool) { return stateV3DocumentAdmission{}, false }`),
		"collector.go": parseAuthorityFixture(t, `package strictcollector
			func (s *session) installOrdinary(ctx context.Context, deadline time.Time) bool { s.ordinary = &ordinaryOperation{}; return true }
			func (s *session) Close() { s.ordinary = nil }
			func (s *session) settleFixed(request fixedRequest) (handle, Result) { _ = s.ordinary; return handle{}, Result{} }
			func (s *session) ReadBlobForTreeEntry(ctx context.Context, prior handle, index int, deadline time.Time) (handle, Result) { return handle{}, Result{} }
			func (s *session) readAssemblyBlob(prior handle, index int) (handle, Result) { return handle{}, Result{} }`),
		"compact_admission.go": parseAuthorityFixture(t, `package strictcollector
			func (s *session) readAssemblyCompactBlob(generation uint64, ordinal uint16, document int) (handle, Result) { return handle{}, Result{} }`),
	}
}

func parseAuthorityFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func exprName(expr ast.Expr) string {
	identifier, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}
