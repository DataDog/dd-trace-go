// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/sqlsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptracemock"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	_ "modernc.org/sqlite"
)

func TestSQLSecurityCheckedContext(t *testing.T) {
	t.Setenv("DD_APPSEC_RASP_ENABLED", "true")
	testutils.StartAppSec(t)
	ctx := context.Background()
	for _, name := range []string{"pgx", "mysql", "sqlserver", "custom-driver-name"} {
		checked, err := checkQuerySecurity(ctx, "SELECT 1", name)
		require.NoError(t, err)
		require.Equal(t, ctx, checked, "background calls must not allocate a marker")
		require.Zero(t, testing.AllocsPerRun(100, func() {
			_, _ = checkQuerySecurity(ctx, "SELECT 1", name)
		}))
	}

	parent := dyngo.NewRootOperation()
	ctx = dyngo.RegisterOperation(ctx, parent)
	var calls []sqlsec.SQLOperationArgs
	dyngo.On(parent, func(_ *sqlsec.SQLOperation, args sqlsec.SQLOperationArgs) { calls = append(calls, args) })
	checked, err := checkQuerySecurity(ctx, "SELECT 1", "pgx")
	require.NoError(t, err)
	sqlsec.MonitorSQLOperation(checked, "/* dbm */ SELECT 1", "postgresql")
	require.Len(t, calls, 1)
	require.False(t, calls[0].MonitorOnly)
	_, err = checkQuerySecurity(ctx, "SELECT 1", "pgx")
	require.NoError(t, err)
	require.Len(t, calls, 2)
}

// monitoringSQLDriver models the native monitoring hooks without an integration dependency.
// Only the context-aware methods used below are implemented.
type monitoringSQLDriver struct {
	driver.Conn
}

func (*monitoringSQLDriver) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	sqlsec.MonitorSQLOperation(ctx, query, "postgresql")
	return driver.RowsAffected(1), nil
}

func (*monitoringSQLDriver) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	sqlsec.MonitorSQLOperation(ctx, query, "postgresql")
	return nil, nil
}

type monitoringSQLStmt struct {
	driver.Stmt
	query string
}

func (s *monitoringSQLStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return (&monitoringSQLDriver{}).ExecContext(ctx, s.query, args)
}

func (s *monitoringSQLStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return (&monitoringSQLDriver{}).QueryContext(ctx, s.query, args)
}

func TestSQLSecurityNativeMonitoring(t *testing.T) {
	t.Setenv("DD_APPSEC_RASP_ENABLED", "true")
	testutils.StartAppSec(t)
	cfg := new(config)
	defaults(cfg, "pgx", nil)
	params := &traceParams{cfg: cfg, driverName: "pgx", spanCfg: newSpanConfig(cfg, "pgx")}
	conn := &TracedConn{Conn: &monitoringSQLDriver{}, traceParams: params}
	for _, query := range []string{"SELECT 1", "injected SQL"} {
		for _, operation := range []string{"exec", "query", "prepared-exec", "prepared-query"} {
			t.Run(query+"/"+operation, func(t *testing.T) {
				parent := dyngo.NewRootOperation()
				ctx := dyngo.RegisterOperation(context.Background(), parent)
				var calls []sqlsec.SQLOperationArgs
				dyngo.On(parent, func(op *sqlsec.SQLOperation, args sqlsec.SQLOperationArgs) {
					calls = append(calls, args)
					if args.Query == "injected SQL" && !args.MonitorOnly {
						dyngo.EmitData(op, &events.BlockingSecurityEvent{})
					}
				})
				stmt := &tracedStmt{Stmt: &monitoringSQLStmt{query: query}, traceParams: params, ctx: ctx, query: query}
				var err error
				switch operation {
				case "exec":
					_, err = conn.ExecContext(ctx, query, nil)
				case "query":
					_, err = conn.QueryContext(ctx, query, nil)
				case "prepared-exec":
					_, err = stmt.ExecContext(ctx, nil)
				case "prepared-query":
					_, err = stmt.QueryContext(ctx, nil)
				}
				prepared := operation == "prepared-exec" || operation == "prepared-query"
				if query == "injected SQL" && !prepared {
					require.True(t, events.IsSecurityError(err))
				} else {
					require.NoError(t, err)
				}
				require.Len(t, calls, 1, "nested monitoring must not duplicate the outer check")
				require.Equal(t, prepared, calls[0].MonitorOnly)
			})
		}
	}
}

func prepareSQLDB(nbEntries int) (*sql.DB, error) {
	const tables = `
CREATE TABLE user (
   id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
   name  text NOT NULL,
   email text NOT NULL,
   password text NOT NULL
);
CREATE TABLE product (
   id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
   name  text NOT NULL,
   category  text NOT NULL,
   price  int NOT NULL
);
`
	db, err := Open("sqlite", ":memory:")
	if err != nil {
		log.Fatalln("unexpected sqltrace.Open error:", err)
	}

	if _, err := db.Exec(tables); err != nil {
		return nil, err
	}

	for i := 0; i < nbEntries; i++ {
		_, err := db.Exec(
			"INSERT INTO user (name, email, password) VALUES (?, ?, ?)",
			fmt.Sprintf("User#%d", i),
			fmt.Sprintf("user%d@mail.com", i),
			fmt.Sprintf("secret-password#%d", i))
		if err != nil {
			return nil, err
		}

		_, err = db.Exec(
			"INSERT INTO product (name, category, price) VALUES (?, ?, ?)",
			fmt.Sprintf("Product %d", i),
			"sneaker",
			rand.Intn(500))
		if err != nil {
			return nil, err
		}
	}

	return db, nil
}

func TestRASPSQLi(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/rasp.json")
	testutils.StartAppSec(t)

	if !instr.AppSecRASPEnabled() {
		t.Skip("RASP needs to be enabled for this test")
	}
	db, err := prepareSQLDB(10)
	require.NoError(t, err)

	// Setup the http server
	mux := httptracemock.NewServeMux()
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		// Subsequent spans inherit their parent from context.
		q := r.URL.Query().Get("query")
		rows, err := db.QueryContext(r.Context(), q)
		if events.IsSecurityError(err) {
			return
		}
		if err == nil {
			rows.Close()
		}
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		// Subsequent spans inherit their parent from context.
		q := r.URL.Query().Get("query")
		_, err := db.ExecContext(r.Context(), q)
		if events.IsSecurityError(err) {
			return
		}
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for name, tc := range map[string]struct {
		query string
		err   error
	}{
		"no-error": {
			query: url.QueryEscape("SELECT 1"),
		},
		"injection/SELECT": {
			query: url.QueryEscape("SELECT * FROM users WHERE user=\"\" UNION ALL SELECT NULL;version()--"),
			err:   &events.BlockingSecurityEvent{},
		},
		"injection/UPDATE": {
			query: url.QueryEscape("UPDATE users SET pwd = \"root\" WHERE id = \"\" OR 1 = 1--"),
			err:   &events.BlockingSecurityEvent{},
		},
		"injection/EXEC": {
			query: url.QueryEscape("EXEC version(); DROP TABLE users--"),
			err:   &events.BlockingSecurityEvent{},
		},
	} {
		for _, endpoint := range []string{"/query", "/exec"} {
			t.Run(name+endpoint, func(t *testing.T) {
				// Start tracer and appsec
				mt := mocktracer.Start()
				defer mt.Stop()

				req, err := http.NewRequest("POST", srv.URL+endpoint+"?query="+tc.query, nil)
				require.NoError(t, err)
				res, err := srv.Client().Do(req)
				require.NoError(t, err)
				defer res.Body.Close()

				spans := mt.FinishedSpans()

				require.Len(t, spans, 2)

				if tc.err != nil {
					require.Equal(t, 403, res.StatusCode)

					for _, sp := range spans {
						switch sp.OperationName() {
						case "http.request":
							require.Contains(t, sp.Tag("_dd.appsec.json"), "rasp-942-100")
						case "sqlite.query":
							require.NotContains(t, sp.Tags(), "error")
						}
					}
				} else {
					require.Equal(t, 200, res.StatusCode)
				}

			})
		}
	}
}
