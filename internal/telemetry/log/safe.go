// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

// SafeError represents a sanitized error for secure telemetry logging.
type SafeError struct {
	errType       string
	redactedStack []redactedFrame
}

type redactedFrame struct {
	function  string
	file      string
	line      int
	frameType frameType
}

type frameType string

const (
	frameTypeDatadog    frameType = "datadog"
	frameTypeRuntime    frameType = "runtime"
	frameTypeThirdParty frameType = "third_party"
	frameTypeCustomer   frameType = "customer"

	maxStackFrames       = 50
	stackSkipFrames      = 3
	datadogPackagePrefix = "github.com/DataDog/dd-trace-go"
	nilErrorType         = "<nil>"
	redactedPlaceholder  = "REDACTED"
)

// TODO: Dynamically generate knownThirdPartyLibraries from contrib/ directory structure at build time
// This should scan contrib/*/go.mod files and extract third-party library patterns automatically
var knownThirdPartyLibraries = []string{
	// Cloud providers
	"cloud.google.com/go/",
	"github.com/aws/aws-sdk-go",

	// Web frameworks
	"github.com/gin-gonic/gin",
	"github.com/gorilla/mux",
	"github.com/go-chi/chi",
	"github.com/labstack/echo",
	"github.com/gofiber/fiber",
	"github.com/valyala/fasthttp",
	"github.com/urfave/negroni",
	"github.com/julienschmidt/httprouter",
	"github.com/dimfeld/httptreemux",
	"github.com/emicklei/go-restful",

	// Databases
	"go.mongodb.org/mongo-driver",
	"github.com/go-redis/redis",
	"github.com/redis/go-redis",
	"github.com/redis/rueidis",
	"github.com/valkey-io/valkey-go",
	"github.com/gomodule/redigo",
	"github.com/gocql/gocql",
	"github.com/go-pg/pg",
	"github.com/jackc/pgx",
	"github.com/jmoiron/sqlx",
	"github.com/go-sql-driver/mysql",
	"github.com/lib/pq",
	"github.com/denisenkom/go-mssqldb",
	"github.com/globalsign/mgo",
	"github.com/syndtr/goleveldb",
	"github.com/tidwall/buntdb",
	"gopkg.in/olivere/elastic",
	"github.com/elastic/go-elasticsearch",

	// Message queues
	"github.com/Shopify/sarama",
	"github.com/IBM/sarama",
	"github.com/segmentio/kafka-go",
	"github.com/confluentinc/confluent-kafka-go",

	// GraphQL
	"github.com/99designs/gqlgen",
	"github.com/graph-gophers/graphql-go",
	"github.com/graphql-go/graphql",

	// Other integrations
	"github.com/hashicorp/consul",
	"github.com/hashicorp/vault",
	"github.com/bradfitz/gomemcache",
	"github.com/miekg/dns",
	"github.com/twitchtv/twirp",
	"github.com/sirupsen/logrus",
	"github.com/envoyproxy/go-control-plane",
	"k8s.io/api",
	"k8s.io/apimachinery",
}

// TODO: Dynamically generate standardLibraryPrefixes from Go's standard library at build time
// This should use go/build or similar to get the complete list of standard library packages
var standardLibraryPrefixes = []string{
	"runtime.",
	"net/",
	"net.",
	"encoding/",
	"encoding.",
	"crypto/",
	"crypto.",
	"database/",
	"database.",
	"fmt.",
	"log.",
	"os.",
	"io.",
	"strings.",
	"strconv.",
	"time.",
	"context.",
	"reflect.",
	"syscall.",
	"sync.",
	"sort.",
	"regexp.",
	"path/",
	"path.",
	"mime/",
	"mime.",
	"html/",
	"html.",
	"errors.",
	"bufio.",
	"bytes.",
	"compress/",
	"compress.",
	"container/",
	"container.",
	"debug/",
	"debug.",
	"go/",
	"go.",
	"hash/",
	"hash.",
	"image/",
	"image.",
	"index/",
	"index.",
	"math/",
	"math.",
	"text/",
	"text.",
	"unicode/",
	"unicode.",
}

// NewSafeError creates a SafeError from a regular error with stack trace redaction
func NewSafeError(err error) SafeError {
	if err == nil {
		return SafeError{errType: nilErrorType}
	}

	safeErr := SafeError{
		errType: errorType(err),
	}

	// Capture and redact stack trace
	safeErr.redactedStack = captureRedactedStack()

	return safeErr
}

// LogValue implements slog.LogValuer to provide secure logging representation
func (e SafeError) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("error_type", e.errType),
	}

	if len(e.redactedStack) > 0 {
		attrs = append(attrs, slog.Any("stack", e.redactedStack))
	}

	return slog.GroupValue(attrs...)
}

// errorType extracts the error type without exposing the error message
func errorType(err error) string {
	if err == nil {
		return nilErrorType
	}

	errType := reflect.TypeOf(err)
	if errType.Kind() == reflect.Ptr {
		errType = errType.Elem()
	}

	if errType.PkgPath() != "" {
		return errType.PkgPath() + "." + errType.Name()
	}
	return errType.Name()
}

// captureRedactedStack captures the current stack trace and redacts customer code
func captureRedactedStack() []redactedFrame {
	pcs := make([]uintptr, maxStackFrames)
	n := runtime.Callers(stackSkipFrames, pcs)

	frames := runtime.CallersFrames(pcs[:n])
	var redactedFrames []redactedFrame

	for {
		frame, more := frames.Next()

		fType := classifyFrame(frame)
		redactedFrame := redactedFrame{
			frameType: fType,
		}

		// Include details for non-customer frames, redact customer frames
		if fType != frameTypeCustomer {
			redactedFrame.function = frame.Function
			redactedFrame.file = frame.File
			redactedFrame.line = frame.Line
		} else {
			// Customer code frames are completely redacted for security
			redactedFrame.function = redactedPlaceholder
			redactedFrame.file = redactedPlaceholder
			redactedFrame.line = 0
		}

		redactedFrames = append(redactedFrames, redactedFrame)

		if !more {
			break
		}
	}

	return redactedFrames
}

// classifyFrame determines the type of a stack frame for redaction purposes
func classifyFrame(frame runtime.Frame) frameType {
	fn := frame.Function

	// 1. Datadog library code - always show
	if strings.Contains(fn, datadogPackagePrefix) {
		return frameTypeDatadog
	}

	// 2. Go runtime and standard library - always show
	if isStandardLibrary(fn) {
		return frameTypeRuntime
	}

	// 3. Known third-party libraries from contrib/ - show
	for _, lib := range knownThirdPartyLibraries {
		if strings.Contains(fn, lib) {
			return frameTypeThirdParty
		}
	}

	// 4. Everything else is customer code - redact completely
	return frameTypeCustomer
}

// isStandardLibrary checks if a function is from Go's standard library
func isStandardLibrary(fn string) bool {
	// Standard library packages don't have dots in their import path before the first slash
	// Examples: runtime.*, net/http.*, encoding/json.*
	parts := strings.Split(fn, "/")
	if len(parts) == 0 {
		return false
	}

	firstPart := parts[0]

	// Special case: main package is customer code, not stdlib
	if strings.HasPrefix(fn, "main.") {
		return false
	}

	// Built-in packages like runtime (but not main)
	if !strings.Contains(firstPart, ".") {
		return true
	}
	for _, prefix := range standardLibraryPrefixes {
		if strings.HasPrefix(fn, prefix) {
			return true
		}
	}

	return false
}

// SafeSlice provides secure logging for slice/array types
type SafeSlice struct {
	items     []string
	count     int
	truncated bool
}

// NewSafeSlice creates a SafeSlice from any slice, converting items to strings
func NewSafeSlice[T any](items []T) SafeSlice {
	return NewSafeSliceWithLimit(items, 100)
}

// NewSafeSliceWithLimit creates a SafeSlice with custom item limit
func NewSafeSliceWithLimit[T any](items []T, maxItems int) SafeSlice {
	stringItems := make([]string, 0, min(len(items), maxItems))

	for i, item := range items {
		if i >= maxItems {
			break
		}

		// Convert item to string safely - only explicit conversions allowed
		var str string
		switch v := any(item).(type) {
		case fmt.Stringer:
			str = v.String()
		case string:
			str = v
		case int:
			str = strconv.Itoa(v)
		case int64:
			str = strconv.FormatInt(v, 10)
		case bool:
			str = strconv.FormatBool(v)
		case float64:
			str = strconv.FormatFloat(v, 'g', -1, 64)
		default:
			str = "<unsupported-type>"
		}
		stringItems = append(stringItems, str)
	}

	return SafeSlice{
		items:     stringItems,
		count:     len(items),
		truncated: len(items) > maxItems,
	}
}

// LogValue implements slog.LogValuer for secure slice logging
func (s SafeSlice) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.Int("count", s.count),
		slog.Any("items", s.items),
	}

	if s.truncated {
		attrs = append(attrs, slog.Bool("truncated", true))
	}

	return slog.GroupValue(attrs...)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
