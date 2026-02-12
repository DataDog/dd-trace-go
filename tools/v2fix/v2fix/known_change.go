// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"golang.org/x/tools/go/analysis"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

const (
	v1ImportPrefix        = "gopkg.in/DataDog/dd-trace-go.v1"
	v1ContribImportPrefix = v1ImportPrefix + "/contrib/"
	v2ImportPrefix        = "github.com/DataDog/dd-trace-go/v2"
	v2ContribImportPrefix = "github.com/DataDog/dd-trace-go/contrib/"
)

// v2ContribModulePaths contains contrib module roots where /v2 must be inserted.
// It mirrors the contrib module layout in this repository (see contrib/**/go.mod).
// We could use instrumentation.GetPackages() to get the list of packages, but it would be
// more complex to derive the v2 import path from TracedPackage field.
var v2ContribModulePaths = []string{
	"99designs/gqlgen",
	"IBM/sarama",
	"Shopify/sarama",
	"aws/aws-sdk-go",
	"aws/aws-sdk-go-v2",
	"aws/datadog-lambda-go",
	"bradfitz/gomemcache",
	"cloud.google.com/go/pubsub.v1",
	"cloud.google.com/go/pubsub.v2",
	"confluentinc/confluent-kafka-go/kafka",
	"confluentinc/confluent-kafka-go/kafka.v2",
	"database/sql",
	"dimfeld/httptreemux.v5",
	"elastic/go-elasticsearch.v6",
	"emicklei/go-restful.v3",
	"envoyproxy/go-control-plane",
	"gin-gonic/gin",
	"globalsign/mgo",
	"go-chi/chi",
	"go-chi/chi.v5",
	"go-pg/pg.v10",
	"go-redis/redis",
	"go-redis/redis.v7",
	"go-redis/redis.v8",
	"go.mongodb.org/mongo-driver",
	"go.mongodb.org/mongo-driver.v2",
	"gocql/gocql",
	"gofiber/fiber.v2",
	"gomodule/redigo",
	"google.golang.org/api",
	"google.golang.org/api/internal/gen_endpoints",
	"google.golang.org/grpc",
	"gorilla/mux",
	"gorm.io/gorm.v1",
	"graph-gophers/graphql-go",
	"graphql-go/graphql",
	"haproxy/stream-processing-offload",
	"hashicorp/consul",
	"hashicorp/vault",
	"jackc/pgx.v5",
	"jmoiron/sqlx",
	"julienschmidt/httprouter",
	"k8s.io/client-go",
	"k8s.io/gateway-api",
	"labstack/echo.v4",
	"log/slog",
	"mark3labs/mcp-go",
	"miekg/dns",
	"modelcontextprotocol/go-sdk",
	"net/http",
	"olivere/elastic.v5",
	"redis/go-redis.v9",
	"redis/rueidis",
	"segmentio/kafka-go",
	"sirupsen/logrus",
	"syndtr/goleveldb",
	"tidwall/buntdb",
	"twitchtv/twirp",
	"uptrace/bun",
	"urfave/negroni",
	"valkey-io/valkey-go",
	"valyala/fasthttp",
}

// KnownChange models code expressions that must be changed to migrate to v2.
// It is defined by a set of probes that must be true to report the analyzed expression.
// It also contains a message function that returns a string describing the change.
// The probes are evaluated in order, and the first one that returns false
// will cause the expression to be ignored.
// A probe can store information in the context, which is available to the following ones.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type KnownChange interface {
	fmt.Stringer

	// Context returns the context associated with the known change.
	Context() context.Context

	// Fixes returns a list of fixes that can be applied to the analyzed expression.
	Fixes() []analysis.SuggestedFix

	// Probes returns a list of probes that must be true to report the analyzed expression.
	Probes() []Probe

	// SetContext updates the context with the given value.
	SetContext(context.Context)

	// SetNode updates the node with the given value.
	SetNode(ast.Node)

	// Clone creates a fresh copy of this KnownChange for thread-safe concurrent use.
	Clone() KnownChange
}

type contextHandler struct {
	ctx context.Context
}

func (c contextHandler) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *contextHandler) SetContext(ctx context.Context) {
	c.ctx = ctx
}

type nodeHandler struct {
	node ast.Node
}

func (n *nodeHandler) SetNode(node ast.Node) {
	n.node = node
}

type defaultKnownChange struct {
	contextHandler
	nodeHandler
}

func (d defaultKnownChange) End() token.Pos {
	end, ok := d.ctx.Value(endKey).(token.Pos)
	if ok {
		return end
	}
	if d.node == nil {
		return token.NoPos
	}
	return d.node.End()
}

func (d defaultKnownChange) Pos() token.Pos {
	pos, ok := d.ctx.Value(posKey).(token.Pos)
	if ok {
		return pos
	}
	if d.node == nil {
		return token.NoPos
	}
	return d.node.Pos()
}

func (d defaultKnownChange) pkgPrefix() string {
	pkg, _ := d.ctx.Value(pkgPrefixKey).(string)
	return pkg
}

func eval(k KnownChange, n ast.Node, pass *analysis.Pass) bool {
	// Reset context for each node evaluation to prevent data races
	// when multiple goroutines analyze different packages concurrently.
	k.SetContext(context.Background())
	for _, p := range k.Probes() {
		ctx, ok := p(k.Context(), n, pass)
		if !ok {
			return false
		}
		k.SetContext(ctx)
	}
	k.SetNode(n)
	return true
}

func rewriteV1ImportPath(path string) string {
	if contribPath, ok := strings.CutPrefix(path, v1ContribImportPrefix); ok {
		return rewriteV1ContribImportPath(contribPath)
	}
	return strings.Replace(path, v1ImportPrefix, v2ImportPrefix, 1)
}

func rewriteV1ContribImportPath(contribPath string) string {
	modulePath := contribPath
	subpkgPath := ""
	longestMatch := ""
	for _, candidate := range v2ContribModulePaths {
		if contribPath != candidate && !strings.HasPrefix(contribPath, candidate+"/") {
			continue
		}
		if len(candidate) > len(longestMatch) {
			longestMatch = candidate
		}
	}
	if longestMatch != "" {
		modulePath = longestMatch
		subpkgPath = strings.TrimPrefix(contribPath, longestMatch)
		subpkgPath = strings.TrimPrefix(subpkgPath, "/")
	}

	path := v2ContribImportPrefix + modulePath + "/v2"
	if subpkgPath != "" {
		path += "/" + subpkgPath
	}
	return path
}

type V1ImportURL struct {
	defaultKnownChange
}

func (V1ImportURL) Clone() KnownChange {
	return &V1ImportURL{}
}

func (c V1ImportURL) Fixes() []analysis.SuggestedFix {
	path := c.ctx.Value(pkgPathKey).(string)
	if path == "" {
		return nil
	}

	path = rewriteV1ImportPath(path)

	return []analysis.SuggestedFix{
		{
			Message: "update import URL to v2",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "%q", path),
				},
			},
		},
	}
}

func (V1ImportURL) Probes() []Probe {
	return []Probe{
		IsImport,
		IsV1Import,
	}
}

func (V1ImportURL) String() string {
	return "import URL needs to be updated"
}

type DDTraceTypes struct {
	defaultKnownChange
}

func (DDTraceTypes) Clone() KnownChange {
	return &DDTraceTypes{}
}

func (c DDTraceTypes) Fixes() []analysis.SuggestedFix {
	// Skip fix if array length couldn't be rendered (avoid corrupting types)
	if skip, _ := c.ctx.Value(skipFixKey).(bool); skip {
		return nil
	}

	// Get the type name from declaredTypeKey, handling both *types.Named and *types.Alias
	// Guard against nil or wrong type to avoid panic on ill-typed code
	typ, ok := c.ctx.Value(declaredTypeKey).(types.Type)
	if !ok || typ == nil {
		return nil
	}
	typeObj := typeNameFromType(typ)
	if typeObj == nil {
		return nil
	}

	// Get the type prefix (*, [], [N]) if the original type was a composite type
	typePrefix, _ := c.ctx.Value(typePrefixKey).(string)

	pkg := c.pkgPrefix()
	if pkg == "" {
		return nil
	}
	return []analysis.SuggestedFix{
		{
			Message: "the declared type is in the ddtrace/tracer package now",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "%s%s.%s", typePrefix, pkg, typeObj.Name()),
				},
			},
		},
	}
}

func (DDTraceTypes) Probes() []Probe {
	return []Probe{
		Or(
			// TODO: add this in a types registration function used to
			// filter by, reduce iterations, and drop some probes.
			Is[*ast.ValueSpec],
			Is[*ast.Field],
		),
		ImportedFrom("gopkg.in/DataDog/dd-trace-go.v1"),
		// Use HasBaseType to also exclude composite types like *SpanContext, []SpanContext
		Not(HasBaseType[ddtrace.SpanContext]()),
	}
}

func (DDTraceTypes) String() string {
	return "the declared type is in the ddtrace/tracer package now"
}

type TracerStructs struct {
	defaultKnownChange
}

func (TracerStructs) Clone() KnownChange {
	return &TracerStructs{}
}

func (c TracerStructs) Fixes() []analysis.SuggestedFix {
	// Use the stored type expression string to preserve original qualifier/alias (e.g., "tracer.Span" vs "tr.Span")
	typeExprStr, ok := c.ctx.Value(typeExprStrKey).(string)
	if !ok || typeExprStr == "" {
		// Fallback to building from declared type (handles both *types.Named and *types.Alias)
		typ, ok := c.ctx.Value(declaredTypeKey).(types.Type)
		if !ok {
			return nil
		}
		typeObj := typeNameFromType(typ)
		if typeObj == nil {
			return nil
		}
		typeExprStr = fmt.Sprintf("%s.%s", typeObj.Pkg().Name(), typeObj.Name())
	}
	return []analysis.SuggestedFix{
		{
			Message: "the declared type is now a struct, you need to use a pointer",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "*%s", typeExprStr),
				},
			},
		},
	}
}

func (TracerStructs) Probes() []Probe {
	return []Probe{
		Or(
			Is[*ast.ValueSpec],
			Is[*ast.Field],
		),
		Or(
			DeclaresType[tracer.Span](),
			DeclaresType[tracer.SpanContext](),
		),
	}
}

func (TracerStructs) String() string {
	return "the declared type is now a struct, you need to use a pointer"
}

type WithServiceName struct {
	defaultKnownChange
}

func (WithServiceName) Clone() KnownChange {
	return &WithServiceName{}
}

func (c WithServiceName) Fixes() []analysis.SuggestedFix {
	args, ok := c.ctx.Value(argsKey).([]ast.Expr)
	if !ok || len(args) < 1 {
		return nil
	}

	pkg := c.pkgPrefix()
	if pkg == "" {
		return nil
	}
	return []analysis.SuggestedFix{
		{
			Message: "the function WithServiceName is no longer supported. Use WithService instead",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "%s.WithService(%s)", pkg, exprToString(args[0])),
				},
			},
		},
	}
}

func (c WithServiceName) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		WithFunctionName("WithServiceName"),
	}
}

func (c WithServiceName) String() string {
	return "the function WithServiceName is no longer supported. Use WithService instead"
}

type TraceIDString struct {
	defaultKnownChange
}

func (TraceIDString) Clone() KnownChange {
	return &TraceIDString{}
}

func (c TraceIDString) Fixes() []analysis.SuggestedFix {
	callExpr, ok := c.ctx.Value(callExprKey).(*ast.CallExpr)
	if !ok {
		return nil
	}

	// Guard against non-selector callExpr.Fun (e.g., direct function calls)
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	return []analysis.SuggestedFix{
		{
			Message: "trace IDs are now represented as strings, please use TraceIDLower to keep using 64-bits IDs, although it's recommended to switch to 128-bits with TraceID",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "%s.TraceIDLower()", exprToString(sel.X)),
				},
			},
		},
	}
}

func (c TraceIDString) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		WithFunctionName("TraceID"),
	}
}

func (c TraceIDString) String() string {
	return "trace IDs are now represented as strings, please use TraceIDLower to keep using 64-bits IDs, although it's recommended to switch to 128-bits with TraceID"
}

type WithDogstatsdAddr struct {
	defaultKnownChange
}

func (WithDogstatsdAddr) Clone() KnownChange {
	return &WithDogstatsdAddr{}
}

func (c WithDogstatsdAddr) Fixes() []analysis.SuggestedFix {
	args, ok := c.ctx.Value(argsKey).([]ast.Expr)
	if !ok || len(args) < 1 {
		return nil
	}

	pkg := c.pkgPrefix()
	if pkg == "" {
		return nil
	}
	return []analysis.SuggestedFix{
		{
			Message: "the function WithDogstatsdAddress is no longer supported. Use WithDogstatsdAddr instead",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: fmt.Appendf(nil, "%s.WithDogstatsdAddr(%s)", pkg, exprToString(args[0])),
				},
			},
		},
	}
}

func (c WithDogstatsdAddr) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		WithFunctionName("WithDogstatsdAddress"),
	}
}

func (c WithDogstatsdAddr) String() string {
	return "the function WithDogstatsdAddress is no longer supported. Use WithDogstatsdAddr instead"
}

// DeprecatedSamplingRules handles the transformation of v1 sampling rule
// constructor functions to v2 tracer.Rule struct literals.
type DeprecatedSamplingRules struct {
	defaultKnownChange
}

func (DeprecatedSamplingRules) Clone() KnownChange {
	return &DeprecatedSamplingRules{}
}

func (c DeprecatedSamplingRules) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		Or(
			WithFunctionName("ServiceRule"), // Sets funcNameKey
			WithFunctionName("NameRule"),
			WithFunctionName("NameServiceRule"),
			WithFunctionName("RateRule"),
			WithFunctionName("TagsResourceRule"),
			WithFunctionName("SpanNameServiceRule"),
			WithFunctionName("SpanNameServiceMPSRule"),
			WithFunctionName("SpanTagsResourceRule"),
		),
	}
}

func (c DeprecatedSamplingRules) Fixes() []analysis.SuggestedFix {
	fn, ok := c.ctx.Value(fnKey).(*types.Func)
	if !ok || fn == nil {
		return nil
	}

	args, ok := c.ctx.Value(argsKey).([]ast.Expr)
	if !ok {
		return nil
	}

	pkg := c.pkgPrefix()
	if pkg == "" {
		return nil
	}

	// Pre-render all args to strings once; bail if any can't be rendered
	// (e.g. binary expressions like 1.0/2 produce empty values).
	argStrs := make([]string, len(args))
	for i, arg := range args {
		s := exprToString(arg)
		if s == "" {
			return nil
		}
		argStrs[i] = s
	}

	var parts []string

	switch fn.Name() {
	case "ServiceRule":
		if len(args) < 2 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("ServiceGlob: %s", argStrs[0]))
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[1]))
	case "NameRule":
		if len(args) < 2 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("NameGlob: %s", argStrs[0]))
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[1]))
	case "RateRule":
		if len(args) < 1 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[0]))
	case "NameServiceRule", "SpanNameServiceRule":
		if len(args) < 3 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("NameGlob: %s", argStrs[0]))
		parts = append(parts, fmt.Sprintf("ServiceGlob: %s", argStrs[1]))
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[2]))
	case "SpanNameServiceMPSRule":
		if len(args) < 4 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("NameGlob: %s", argStrs[0]))
		parts = append(parts, fmt.Sprintf("ServiceGlob: %s", argStrs[1]))
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[2]))
		parts = append(parts, fmt.Sprintf("MaxPerSecond: %s", argStrs[3]))
	case "TagsResourceRule", "SpanTagsResourceRule":
		if len(args) < 5 {
			return nil
		}
		parts = append(parts, fmt.Sprintf("Tags: %s", argStrs[0]))
		parts = append(parts, fmt.Sprintf("ResourceGlob: %s", argStrs[1]))
		parts = append(parts, fmt.Sprintf("NameGlob: %s", argStrs[2]))
		parts = append(parts, fmt.Sprintf("ServiceGlob: %s", argStrs[3]))
		parts = append(parts, fmt.Sprintf("Rate: %s", argStrs[4]))
	}

	var ruleType string
	switch fn.Name() {
	case "SpanNameServiceMPSRule", "SpanTagsResourceRule", "SpanNameServiceRule":
		ruleType = "Span"
	default:
		ruleType = "Trace"
	}

	// Qualify Rule with the package prefix to avoid compilation errors
	newText := fmt.Sprintf("%s.%sSamplingRules(%s.Rule{%s})", pkg, ruleType, pkg, strings.Join(parts, ", "))

	return []analysis.SuggestedFix{
		{
			Message: "a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(newText),
				},
			},
		},
	}
}

func (c DeprecatedSamplingRules) String() string {
	return "a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal"
}

// ChildOfStartChild handles the transformation of tracer.StartSpan("op", tracer.ChildOf(parent.Context()))
// to parent.StartChild("op"). This is a complex structural change.
type ChildOfStartChild struct {
	defaultKnownChange
}

func (ChildOfStartChild) Clone() KnownChange {
	return &ChildOfStartChild{}
}

func (c ChildOfStartChild) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"),
		WithFunctionName("StartSpan"),
		HasChildOfOption,
	}
}

func (c ChildOfStartChild) Fixes() []analysis.SuggestedFix {
	if skip, _ := c.ctx.Value(skipFixKey).(bool); skip {
		return nil
	}

	args, ok := c.ctx.Value(argsKey).([]ast.Expr)
	if !ok || len(args) < 2 {
		return nil
	}

	// First arg is the operation name — only auto-fix when it is a simple
	// literal; non-literal expressions (e.g. "a"+suffix) are left as
	// diagnostic-only because the rewrite may not be safe.
	opName := args[0]
	if _, isLit := opName.(*ast.BasicLit); !isLit {
		return nil
	}
	opNameStr := exprToString(opName)
	if opNameStr == "" {
		return nil
	}

	// Get the parent expression from context (set by HasChildOfOption)
	parentExpr, ok := c.ctx.Value(childOfParentKey).(string)
	if !ok || parentExpr == "" {
		return nil
	}

	// Get the other options (excluding ChildOf) from context
	otherOpts, _ := c.ctx.Value(childOfOtherOptsKey).([]string)

	var newText string
	if len(otherOpts) > 0 {
		newText = fmt.Sprintf("%s.StartChild(%s, %s)", parentExpr, opNameStr, strings.Join(otherOpts, ", "))
	} else {
		newText = fmt.Sprintf("%s.StartChild(%s)", parentExpr, opNameStr)
	}

	return []analysis.SuggestedFix{
		{
			Message: "use StartChild instead of StartSpan with ChildOf",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(newText),
				},
			},
		},
	}
}

func (c ChildOfStartChild) String() string {
	return "use StartChild instead of StartSpan with ChildOf"
}

// AppSecLoginEvents handles the renaming of appsec login event functions.
// TrackUserLoginSuccessEvent → TrackUserLoginSuccess
// TrackUserLoginFailureEvent → TrackUserLoginFailure
type AppSecLoginEvents struct {
	defaultKnownChange
}

func (AppSecLoginEvents) Clone() KnownChange {
	return &AppSecLoginEvents{}
}

func (c AppSecLoginEvents) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		Or(
			WithFunctionName("TrackUserLoginSuccessEvent"),
			WithFunctionName("TrackUserLoginFailureEvent"),
		),
	}
}

func (c AppSecLoginEvents) Fixes() []analysis.SuggestedFix {
	// Neither function can be safely auto-fixed:
	// - TrackUserLoginSuccessEvent → TrackUserLoginSuccess has a new 'login' parameter
	// - TrackUserLoginFailureEvent → TrackUserLoginFailure's second parameter changed
	//   from user ID to login value
	// Both are diagnostic-only until a safe argument mapping is available.
	return nil
}

func (c AppSecLoginEvents) String() string {
	return "appsec login event functions have been renamed (remove 'Event' suffix)"
}

// DeprecatedWithPrioritySampling warns about usage of WithPrioritySampling which has been removed.
// Priority sampling is now enabled by default.
type DeprecatedWithPrioritySampling struct {
	defaultKnownChange
}

func (DeprecatedWithPrioritySampling) Clone() KnownChange {
	return &DeprecatedWithPrioritySampling{}
}

func (c DeprecatedWithPrioritySampling) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		WithFunctionName("WithPrioritySampling"),
	}
}

func (c DeprecatedWithPrioritySampling) Fixes() []analysis.SuggestedFix {
	// Warning only - no auto-fix since it should just be removed
	return nil
}

func (c DeprecatedWithPrioritySampling) String() string {
	return "WithPrioritySampling has been removed; priority sampling is now enabled by default"
}

// DeprecatedWithHTTPRoundTripper warns about usage of WithHTTPRoundTripper which has been removed.
type DeprecatedWithHTTPRoundTripper struct {
	defaultKnownChange
}

func (DeprecatedWithHTTPRoundTripper) Clone() KnownChange {
	return &DeprecatedWithHTTPRoundTripper{}
}

func (c DeprecatedWithHTTPRoundTripper) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasV1PackagePath,
		WithFunctionName("WithHTTPRoundTripper"),
	}
}

func (c DeprecatedWithHTTPRoundTripper) Fixes() []analysis.SuggestedFix {
	// Warning only - cannot auto-fix since the API signature changed (RoundTripper vs Client)
	return nil
}

func (c DeprecatedWithHTTPRoundTripper) String() string {
	return "WithHTTPRoundTripper has been removed; use WithHTTPClient instead"
}
