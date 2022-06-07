// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqltest // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"

import (
	"context"
	"database/sql"
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Prepare sets up a table with the given name in both the MySQL and Postgres databases and returns
// a teardown function which will drop it.
func Prepare(tableName string) func() {
	queryDrop := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	queryCreate := fmt.Sprintf("CREATE TABLE %s (id integer NOT NULL DEFAULT '0', name text)", tableName)
	mysql, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	defer mysql.Close()
	if err != nil {
		log.Fatal(err)
	}
	mysql.Exec(queryDrop)
	mysql.Exec(queryCreate)
	postgres, err := sql.Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	defer postgres.Close()
	if err != nil {
		log.Fatal(err)
	}
	postgres.Exec(queryDrop)
	postgres.Exec(queryCreate)
	mssql, err := sql.Open("sqlserver", "sqlserver://sa:myPassw0rd@localhost:1433?database=master")
	defer mssql.Close()
	if err != nil {
		log.Fatal(err)
	}
	mssql.Exec(queryDrop)
	mssql.Exec(queryCreate)
	svc := globalconfig.ServiceName()
	globalconfig.SetServiceName("test-service")
	return func() {
		mysql.Exec(queryDrop)
		postgres.Exec(queryDrop)
		mssql.Exec(queryDrop)
		globalconfig.SetServiceName(svc)
		defer os.Unsetenv("DD_TRACE_SQL_COMMENT_INJECTION_MODE")
	}
}

// RunAll applies a sequence of unit tests to check the correct tracing of sql features.
func RunAll(t *testing.T, cfg *Config) {
	cfg.mockTracer = mocktracer.Start()
	defer cfg.mockTracer.Stop()
	cfg.DB.SetMaxIdleConns(0)

	for name, test := range map[string]func(*Config) func(*testing.T){
		"Connect":       testConnect,
		"Ping":          testPing,
		"Query":         testQuery,
		"Statement":     testStatement,
		"BeginRollback": testBeginRollback,
		"Exec":          testExec,
	} {
		t.Run(name, test(cfg))
	}
}

func testConnect(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		err := cfg.DB.Ping()
		assert.Nil(err)
		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 2)

		span := spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Connect"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
		//assert.Len(cfg.mockTracer.InjectedComments(), 0)
	}
}

func testPing(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		err := cfg.DB.Ping()
		assert.Nil(err)
		spans := cfg.mockTracer.FinishedSpans()
		require.Len(t, spans, 2)

		verifyConnectSpan(spans[0], assert, cfg)

		span := spans[1]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Ping"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
		//assert.Len(cfg.mockTracer.InjectedComments(), 0)
	}
}

func testQuery(cfg *Config) func(*testing.T) {
	var query string
	switch cfg.DriverName {
	case "postgres", "pgx", "mysql":
		query = fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", cfg.TableName)
	case "sqlserver":
		query = fmt.Sprintf("SELECT TOP 5 id, name FROM %s", cfg.TableName)
	}
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		rows, err := cfg.DB.Query(query)
		defer rows.Close()
		assert.Nil(err)

		spans := cfg.mockTracer.FinishedSpans()
		var querySpan mocktracer.Span
		expectedCommentTags := map[string]tagExpectation{
			"ddsn":  {MustBeSet: true, ExpectedValue: "test-service"},
			"ddsp":  {MustBeSet: true, ExpectedValue: "0"},
			"ddsid": {MustBeSet: true},
			"ddtid": {MustBeSet: true},
		}
		if cfg.DriverName == "sqlserver" {
			//The mssql driver doesn't support non-prepared queries so there are 3 spans
			//connect, prepare, and query
			assert.Len(spans, 3)
			span := spans[1]
			cfg.ExpectTags["sql.query_type"] = "Prepare"
			assert.Equal(cfg.ExpectName, span.OperationName())
			for k, v := range cfg.ExpectTags {
				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
			}
			querySpan = spans[2]
			// Since SQLServer runs execute statements by doing a prepare first, the expected comment
			// excludes dynamic tags which can only be injected on non-prepared statements
			expectedCommentTags = map[string]tagExpectation{
				"ddsn":  {MustBeSet: true, ExpectedValue: "test-service"},
				"ddsp":  {MustBeSet: false},
				"ddsid": {MustBeSet: false},
				"ddtid": {MustBeSet: false},
			}
		} else {
			assert.Len(spans, 2)
			querySpan = spans[1]
		}

		verifyConnectSpan(spans[0], assert, cfg)
		cfg.ExpectTags["sql.query_type"] = "Query"
		assert.Equal(cfg.ExpectName, querySpan.OperationName())
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, querySpan.Tag(k), "Value mismatch on tag %s", k)
		}
		assertInjectedComment(t, querySpan, expectedCommentTags)
	}
}

type tagExpectation struct {
	MustBeSet     bool
	ExpectedValue string
}

func assertInjectedComment(t *testing.T, querySpan mocktracer.Span, expectedTags map[string]tagExpectation) {
	q, ok := querySpan.Tag(ext.ResourceName).(string)
	require.True(t, ok, "tag %s should be a string but was %v", ext.ResourceName, q)
	tags, err := extractTags(q)
	require.NoError(t, err)
	for k, e := range expectedTags {
		if e.MustBeSet {
			assert.NotZerof(t, tags[k], "Value must be set on tag %s", k)
		} else {
			assert.Zero(t, tags[k], "Value should not be set on tag %s", k)
		}
		if e.ExpectedValue != "" {
			assert.Equal(t, e.ExpectedValue, tags[k], "Value mismatch on tag %s", k)
		}
	}
}

func extractTags(query string) (map[string]string, error) {
	c, err := findSQLComment(query)
	if err != nil {
		return nil, err
	}

	return extractCommentTags(c)
}

func findSQLComment(query string) (comment string, err error) {
	start := strings.Index(query, "/*")
	if start == -1 {
		return "", nil
	}
	end := strings.Index(query[start:], "*/")
	if end == -1 {
		return "", nil
	}
	c := query[start : end+2]
	spacesTrimmed := strings.TrimSpace(c)
	if !strings.HasPrefix(spacesTrimmed, "/*") {
		return "", fmt.Errorf("comments not in the sqlcommenter format, expected to start with '/*'")
	}
	if !strings.HasSuffix(spacesTrimmed, "*/") {
		return "", fmt.Errorf("comments not in the sqlcommenter format, expected to end with '*/'")
	}
	c = strings.TrimLeft(c, "/*")
	c = strings.TrimRight(c, "*/")
	return strings.TrimSpace(c), nil
}

func extractCommentTags(comment string) (keyValues map[string]string, err error) {
	keyValues = make(map[string]string)
	if err != nil {
		return nil, err
	}
	if comment == "" {
		return keyValues, nil
	}
	tagList := strings.Split(comment, ",")
	for _, t := range tagList {
		k, v, err := extractKeyValue(t)
		if err != nil {
			return nil, err
		} else {
			keyValues[k] = v
		}
	}
	return keyValues, nil
}

func extractKeyValue(tag string) (key string, val string, err error) {
	parts := strings.SplitN(tag, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("tag format invalid, expected 'key=value' but got %s", tag)
	}
	key, err = extractKey(parts[0])
	if err != nil {
		return "", "", err
	}
	val, err = extractValue(parts[1])
	if err != nil {
		return "", "", err
	}
	return key, val, nil
}

func extractKey(keyVal string) (key string, err error) {
	unescaped := unescapeMetaCharacters(keyVal)
	decoded, err := url.PathUnescape(unescaped)
	if err != nil {
		return "", fmt.Errorf("failed to url unescape key: %w", err)
	}

	return decoded, nil
}

func extractValue(rawValue string) (value string, err error) {
	trimmedLeft := strings.TrimLeft(rawValue, "'")
	trimmed := strings.TrimRight(trimmedLeft, "'")

	unescaped := unescapeMetaCharacters(trimmed)
	decoded, err := url.PathUnescape(unescaped)

	if err != nil {
		return "", fmt.Errorf("failed to url unescape value: %w", err)
	}

	return decoded, nil
}

func unescapeMetaCharacters(val string) (unescaped string) {
	return strings.ReplaceAll(val, "\\'", "'")
}

func testStatement(cfg *Config) func(*testing.T) {
	query := "INSERT INTO %s(name) VALUES(%s)"
	switch cfg.DriverName {
	case "postgres", "pgx":
		query = fmt.Sprintf(query, cfg.TableName, "$1")
	case "mysql":
		query = fmt.Sprintf(query, cfg.TableName, "?")
	case "sqlserver":
		query = fmt.Sprintf(query, cfg.TableName, "@p1")
	}
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)
		stmt, err := cfg.DB.Prepare(query)
		assert.Equal(nil, err)

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 3)

		verifyConnectSpan(spans[0], assert, cfg)

		span := spans[1]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Prepare"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}

		assertInjectedComment(t, span, map[string]tagExpectation{
			"ddsn":  {MustBeSet: true, ExpectedValue: "test-service"},
			"ddsp":  {MustBeSet: false},
			"ddsid": {MustBeSet: false},
			"ddtid": {MustBeSet: false},
		})

		cfg.mockTracer.Reset()
		_, err2 := stmt.Exec("New York")
		assert.Equal(nil, err2)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 4)
		span = spans[2]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Exec"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

func testBeginRollback(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		cfg.mockTracer.Reset()
		assert := assert.New(t)

		tx, err := cfg.DB.Begin()
		assert.Equal(nil, err)

		spans := cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 2)

		verifyConnectSpan(spans[0], assert, cfg)

		span := spans[1]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Begin"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}

		cfg.mockTracer.Reset()
		err = tx.Rollback()
		assert.Equal(nil, err)

		spans = cfg.mockTracer.FinishedSpans()
		assert.Len(spans, 1)
		span = spans[0]
		assert.Equal(cfg.ExpectName, span.OperationName())
		cfg.ExpectTags["sql.query_type"] = "Rollback"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
		//assert.Len(cfg.mockTracer.InjectedComments(), 0)
	}
}

func testExec(cfg *Config) func(*testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)
		query := fmt.Sprintf("INSERT INTO %s(name) VALUES('New York')", cfg.TableName)

		parent, ctx := tracer.StartSpanFromContext(context.Background(), "test.parent",
			tracer.ServiceName("test"),
			tracer.ResourceName("parent"),
		)

		cfg.mockTracer.Reset()
		tx, err := cfg.DB.BeginTx(ctx, nil)
		assert.Equal(nil, err)
		_, err = tx.ExecContext(ctx, query)
		assert.Equal(nil, err)
		err = tx.Commit()
		assert.Equal(nil, err)

		parent.Finish() // flush children

		spans := cfg.mockTracer.FinishedSpans()
		//expectedComment := "/*dde='test-env',ddsid='test-span-id',ddsn='test-service',ddsp='0',ddsv='v-test',ddtid='test-trace-id'*/"
		if cfg.DriverName == "sqlserver" {
			//The mssql driver doesn't support non-prepared exec so there are 2 extra spans for the exec:
			//prepare, exec, and then a close
			assert.Len(spans, 7)
			span := spans[2]
			cfg.ExpectTags["sql.query_type"] = "Prepare"
			assert.Equal(cfg.ExpectName, span.OperationName())
			for k, v := range cfg.ExpectTags {
				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
			}
			span = spans[4]
			cfg.ExpectTags["sql.query_type"] = "Close"
			assert.Equal(cfg.ExpectName, span.OperationName())
			for k, v := range cfg.ExpectTags {
				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
			}
			// Since SQLServer runs execute statements by doing a prepare, the expected comment
			// excludes dynamic tags which can only be injected on non-prepared statements
			//expectedComment = "/*dde='test-env',ddsn='test-service',ddsv='v-test'*/"
		} else {
			assert.Len(spans, 5)
		}

		var span mocktracer.Span
		for _, s := range spans {
			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == query {
				span = s
			}
		}
		assert.NotNil(span, "span not found")
		cfg.ExpectTags["sql.query_type"] = "Exec"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
		for _, s := range spans {
			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == "Commit" {
				span = s
			}
		}
		//comments := cfg.mockTracer.InjectedComments()
		//require.Len(t, comments, 1)
		//assert.Equal(expectedComment, comments[0])

		assert.NotNil(span, "span not found")
		cfg.ExpectTags["sql.query_type"] = "Commit"
		for k, v := range cfg.ExpectTags {
			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
		}
	}
}

func verifyConnectSpan(span mocktracer.Span, assert *assert.Assertions, cfg *Config) {
	assert.Equal(cfg.ExpectName, span.OperationName())
	cfg.ExpectTags["sql.query_type"] = "Connect"
	for k, v := range cfg.ExpectTags {
		assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
	}
}

// Config holds the test configuration.
type Config struct {
	*sql.DB
	mockTracer mocktracer.Tracer
	DriverName string
	TableName  string
	ExpectName string
	ExpectTags map[string]interface{}
}
