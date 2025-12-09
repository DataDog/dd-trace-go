// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package sql

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptracemock"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

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
