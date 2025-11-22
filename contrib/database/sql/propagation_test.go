// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/database/sql/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestDBMPropagation(t *testing.T) {
	testCases := []struct {
		name                     string
		opts                     []Option
		callDB                   func(ctx context.Context, db *sql.DB) error
		prepared                 []string
		dsn                      string
		executed                 []*regexp.Regexp
		peerServiceTag           string
		peerServiceCtx           string
		peerServiceCustomOpenTag string
	}{
		{
			name: "prepare",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepare-disabled",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepare-service",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "prepare-full",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:            "postgres://postgres:postgres@127.0.0.1:5432/fakepreparedb?sslmode=disable",
			prepared:       []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',ddh='127.0.0.1',dddb='fakepreparedb',ddprs='test-peer-service'*/ SELECT 1 from DUAL"},
			peerServiceCtx: "test-peer-service",
		},
		{
			name: "query",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query-disabled",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query-service",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "query-full",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:            "postgres://postgres:postgres@127.0.0.1:5432/fakequerydb?sslmode=disable",
			executed:       []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakequerydb',ddprs='test-peer-service'\\*/ SELECT 1 from DUAL")},
			peerServiceCtx: "test-peer-service",
		},
		{
			name: "exec",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec-disabled",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec-service",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec-full",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:            "postgres://postgres:postgres@127.0.0.1:5432/fakeexecdb?sslmode=disable",
			executed:       []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakeexecdb',ddprs='test-peer-service'\\*/ SELECT 1 from DUAL")},
			peerServiceCtx: "test-peer-service",
		},
		{
			name: "exec-full-peer-service-tag",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:            "postgres://postgres:postgres@127.0.0.1:5432/fakeexecdb?sslmode=disable",
			executed:       []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakeexecdb',ddprs='test-peer-service-tag'\\*/ SELECT 1 from DUAL")},
			peerServiceTag: "test-peer-service-tag",
		},
		{
			name: "exec-full-peer-service-custom-tag",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:                      "postgres://postgres:postgres@127.0.0.1:5432/fakeexecdb?sslmode=disable",
			executed:                 []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakeexecdb',ddprs='test-peer-service-custom-tag'\\*/ SELECT 1 from DUAL")},
			peerServiceCustomOpenTag: "test-peer-service-custom-tag",
		},
		{
			name: "exec-full-peer-service-precedence-tag-over-conn-context",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:            "postgres://postgres:postgres@127.0.0.1:5432/fakeexecdb?sslmode=disable",
			executed:       []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakeexecdb',ddprs='test-peer-service-tag'\\*/ SELECT 1 from DUAL")},
			peerServiceCtx: "test-peer-service-ctx",
			peerServiceTag: "test-peer-service-tag",
		},
		{
			name: "exec-full-peer-service-precedence-conn-context-over-open-custom-tag",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			dsn:                      "postgres://postgres:postgres@127.0.0.1:5432/fakeexecdb?sslmode=disable",
			executed:                 []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01',ddh='127.0.0.1',dddb='fakeexecdb',ddprs='test-peer-service-ctx'\\*/ SELECT 1 from DUAL")},
			peerServiceCtx:           "test-peer-service-ctx",
			peerServiceCustomOpenTag: "test-peer-service-custom-tag",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tracer.Start(
				tracer.WithEnv("test-env"),
				tracer.WithServiceVersion("1.0.0"),
				tracer.WithHTTPClient(&http.Client{Transport: &mockRoundTripper{}}),
				tracer.WithLogger(testutils.DiscardLogger()),
			)
			require.NoError(t, err)
			defer tracer.Stop()
			testutils.SetGlobalServiceName(t, "test-service")

			d := &internal.MockDriver{}
			Register("test", d, tc.opts...)
			defer unregister("test")

			dsn := "dn"
			if tc.dsn != "" {
				dsn = tc.dsn
			}
			var options = []Option{}
			if tc.peerServiceCustomOpenTag != "" {
				options = append(options, WithCustomTag(ext.PeerService, tc.peerServiceCustomOpenTag))
			}
			db, err := Open("test", dsn, options...)
			require.NoError(t, err)
			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			if tc.peerServiceCtx != "" {
				vars := map[string]string{
					ext.PeerService: tc.peerServiceCtx,
				}
				ctx = WithSpanTags(ctx, vars)
			}
			if tc.peerServiceTag != "" {
				s.SetTag(ext.PeerService, tc.peerServiceTag)
			}
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			require.Len(t, d.Prepared, len(tc.prepared))
			for i, e := range tc.prepared {
				assert.Equal(t, e, d.Prepared[i])
			}

			require.Len(t, d.Executed, len(tc.executed))
			for i, e := range tc.executed {
				assert.Regexp(t, e, d.Executed[i])
				// the injected span ID should not be the parent's span ID
				assert.NotContains(t, d.Executed[i], "traceparent='00-00000000000000000000000000000001-0000000000000001")
			}
		})
	}
}

func TestDBMPropagationFullOnPqCopy(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("skipping integration test")
	}
	tr := mocktracer.Start()
	defer tr.Stop()

	Register("postgres", &pq.Driver{}, WithDBMPropagation(tracer.DBMPropagationModeFull))
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	require.NoError(t, err)

	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, db.Close())
	})

	db.Exec("DROP TABLE IF EXISTS testsql")
	db.Exec("CREATE TABLE testsql (dn text, name text, sam_account_name text, mail text, primary_group_id text)")
	t.Cleanup(func() {
		db.Exec("DROP TABLE IF EXISTS testsql")
	})

	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()

	s := pq.CopyInSchema("public", "testsql", "dn", "name", "sam_account_name", "mail", "primary_group_id")
	stmt, err := tx.Prepare(s)
	require.NoError(t, err)
	defer stmt.Close()

	_, err = stmt.Exec("dn", "name0", "sam", nil, nil)
	require.NoError(t, err)

	spans := tr.FinishedSpans()
	require.Len(t, spans, 6)
	assert.Equal(t, `COPY "public"."testsql" ("dn", "name", "sam_account_name", "mail", "primary_group_id") FROM STDIN`, spans[5].Tags()[ext.ResourceName])
}

func TestDBMTraceContextTagging(t *testing.T) {
	testCases := []struct {
		name                    string
		opts                    []Option
		callDB                  func(ctx context.Context, db *sql.DB) error
		spanType                string
		traceContextInjectedTag bool
	}{
		{
			name: "prepare",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypePrepare,
			traceContextInjectedTag: false,
		},
		{
			name: "query-disabled",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-service",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-full",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: true,
		},
		{
			name: "exec-disabled",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-service",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-full",
			opts: []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tr := mocktracer.Start()
			defer tr.Stop()

			d := &internal.MockDriver{}
			Register("test", d, tc.opts...)
			defer unregister("test")

			db, err := Open("test", "dn")
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			spans := tr.FinishedSpans()

			sps := spansOfType(spans, tc.spanType)
			for _, s := range sps {
				tags := s.Tags()
				if tc.traceContextInjectedTag {
					assert.Equal(t, "true", tags[keyDBMTraceInjected])
				} else {
					_, ok := tags[keyDBMTraceInjected]
					assert.False(t, ok)
				}
			}
		})
	}
}

func TestDBMPropagation_PreventFullMode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		openDB       func(t *testing.T, opts ...Option) (*sql.DB, error)
		wantFullMode bool
	}{
		{
			name: "sqlserver",
			openDB: func(t *testing.T, opts ...Option) (*sql.DB, error) {
				driverName := "sqlserver"
				// use the mock driver, as the real mssql driver does not implement Execer and Querier interfaces and always falls back
				// to Prepare which always uses service propagation mode, so we can't test whether the DBM propagation mode gets downgraded or not.
				Register(driverName, &internal.MockDriver{}, opts...)
				t.Cleanup(func() { unregister(driverName) })

				dsn := "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
				return Open(driverName, dsn, opts...)
			},
			wantFullMode: false,
		},
		{
			name: "sqlserver-implicit-register",
			openDB: func(t *testing.T, opts ...Option) (*sql.DB, error) {
				driverName := "sqlserver"
				t.Cleanup(func() { unregister(driverName) })

				dsn := "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
				return Open(driverName, dsn, opts...)
			},
			wantFullMode: false,
		},
		{
			name: "sqlserver-dsn",
			openDB: func(t *testing.T, opts ...Option) (*sql.DB, error) {
				driverName := "sqlserver-custom"
				Register(driverName, &internal.MockDriver{}, opts...)
				t.Cleanup(func() { unregister(driverName) })

				dsn := "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
				return Open(driverName, dsn, opts...)
			},
			wantFullMode: false,
		},
		{
			name: "mysql",
			openDB: func(t *testing.T, opts ...Option) (*sql.DB, error) {
				driverName := "mysql"
				Register(driverName, &internal.MockDriver{}, opts...)
				t.Cleanup(func() { unregister(driverName) })

				dsn := "test:test@tcp(127.0.0.1:3306)/test"
				return Open(driverName, dsn, opts...)
			},
			wantFullMode: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := mocktracer.Start()
			defer tr.Stop()

			opts := []Option{WithDBMPropagation(tracer.DBMPropagationModeFull)}
			db, err := tc.openDB(t, opts...)
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			_, err = db.ExecContext(ctx, "SELECT * FROM INFORMATION_SCHEMA.TABLES")
			require.NoError(t, err)
			s.Finish()

			spans := tr.FinishedSpans()
			for _, s := range spansOfType(spans, QueryTypeExec) {
				if !tc.wantFullMode {
					assert.NotContains(t, s.Tags(), keyDBMTraceInjected)
				} else {
					assert.Contains(t, s.Tags(), keyDBMTraceInjected)
				}
			}
		})
	}
}

func TestDBMFullModeUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name            string
		driverName      string
		driver          driver.Driver
		dsn             string
		wantDBSystem    string
		wantUnsupported bool
	}{
		{
			name:            "driver-name-unsupported",
			driverName:      "mssql",
			driver:          nil,
			dsn:             "",
			wantDBSystem:    "SQL Server",
			wantUnsupported: true,
		},
		{
			name:            "driver-name-supported",
			driverName:      "mysql",
			driver:          nil,
			dsn:             "",
			wantDBSystem:    "",
			wantUnsupported: false,
		},
		{
			name:            "driver-type-unsupported",
			driverName:      "mssql-custom-name",
			driver:          &mssql.Driver{},
			dsn:             "",
			wantDBSystem:    "SQL Server",
			wantUnsupported: true,
		},
		{
			name:            "driver-type-supported",
			driverName:      "mysql-custom-name",
			driver:          &mysql.MySQLDriver{},
			dsn:             "",
			wantDBSystem:    "",
			wantUnsupported: false,
		},
		{
			name:            "dsn-unsupported",
			driverName:      "mssql-custom-name",
			driver:          nil,
			dsn:             "sqlserver://username:password@host/instance?param1=value&param2=value",
			wantDBSystem:    "SQL Server",
			wantUnsupported: true,
		},
		{
			name:            "dsn-supported",
			driverName:      "mysql-custom-name",
			driver:          nil,
			dsn:             "username:password@tcp(127.0.0.1:3306)/test",
			wantDBSystem:    "",
			wantUnsupported: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbSystem, unsupported := dbmFullModeUnsupported(tc.driverName, tc.driver, tc.dsn)
			assert.Equal(t, tc.wantDBSystem, dbSystem)
			assert.Equal(t, tc.wantUnsupported, unsupported)
		})
	}
}

func spansOfType(spans []*mocktracer.Span, spanType string) (filtered []*mocktracer.Span) {
	filtered = make([]*mocktracer.Span, 0)
	for _, s := range spans {
		if s.Tag("sql.query_type") == spanType {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString("{}")),
	}, nil
}
