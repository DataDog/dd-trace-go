// package memcache

// import (
// 	"context"
// 	"database/sql"
// 	"fmt"
// 	"log"
// 	"net"
// 	"os"
// 	"testing"
// 	"time"

// 	"github.com/go-sql-driver/mysql"
// 	"github.com/lib/pq"
// 	mssql "github.com/microsoft/go-mssqldb"
// 	"github.com/miekg/dns"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// 	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"
// 	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
// 	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
// 	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
// )

// type Integration struct {
// 	msg      *dns.Msg
// 	mux      *dns.ServeMux
// 	addr     string
// 	numSpans int
// }

// func New() *Integration {
// 	return &Integration{}
// }

// func (i *Integration) Name() string {
// 	return "contrib/miekg/dns"
// }

// func (i *Integration) Init(t *testing.T) func() {
// 	t.Helper()
// 	i.addr = getFreeAddr(t).String()
// 	server := &dns.Server{
// 		Addr:    i.addr,
// 		Net:     "udp",
// 		Handler: dnstrace.WrapHandler(&handler{t: t, ig: i}),
// 	}
// 	// start the traced server
// 	go func() {
// 		require.NoError(t, server.ListenAndServe())
// 	}()
// 	// wait for the server to be ready
// 	waitServerReady(t, server.Addr)
// 	cleanup := func() {
// 		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
// 		defer cancel()
// 		assert.NoError(t, server.ShutdownContext(ctx))
// 	}
// 	return cleanup
// }

// func (i *Integration) GenSpans(t *testing.T) {
// 	t.Helper()
// 	msg := newMessage()
// 	_, err := dnstrace.Exchange(msg, i.addr)
// 	require.NoError(t, err)
// 	i.numSpans++
// }

// func (i *Integration) NumSpans() int {
// 	return i.numSpans
// }

// func newMessage() *dns.Msg {
// 	m := new(dns.Msg)
// 	m.SetQuestion("miek.nl.", dns.TypeMX)
// 	return m
// }

// type handler struct {
// 	t  *testing.T
// 	ig *Integration
// }

// func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
// 	m := new(dns.Msg)
// 	m.SetReply(r)
// 	assert.NoError(h.t, w.WriteMsg(m))
// 	h.ig.numSpans++
// }

// func getFreeAddr(t *testing.T) net.Addr {
// 	li, err := net.Listen("tcp", "127.0.0.1:0")
// 	require.NoError(t, err)
// 	addr := li.Addr()
// 	require.NoError(t, li.Close())
// 	return addr
// }

// func waitServerReady(t *testing.T, addr string) {
// 	ticker := time.NewTicker(100 * time.Millisecond)
// 	defer ticker.Stop()
// 	timeoutChan := time.After(5 * time.Second)
// 	for {
// 		m := new(dns.Msg)
// 		m.SetQuestion("miek.nl.", dns.TypeMX)
// 		_, err := dns.Exchange(m, addr)
// 		if err == nil {
// 			break
// 		}

// 		select {
// 		case <-ticker.C:
// 			continue

// 		case <-timeoutChan:
// 			t.Fatal("timeout waiting for DNS server to be ready")
// 		}
// 	}
// }

// // Prepare sets up a table with the given name in both the MySQL and Postgres databases and returns
// // a teardown function which will drop it.
// func Prepare(tableName string) func() {
// 	queryDrop := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
// 	queryCreate := fmt.Sprintf("CREATE TABLE %s (id integer NOT NULL DEFAULT '0', name text)", tableName)
// 	mysql, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
// 	defer mysql.Close()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	mysql.Exec(queryDrop)
// 	mysql.Exec(queryCreate)
// 	postgres, err := sql.Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
// 	defer postgres.Close()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	postgres.Exec(queryDrop)
// 	postgres.Exec(queryCreate)
// 	mssql, err := sql.Open("sqlserver", "sqlserver://sa:myPassw0rd@localhost:1433?database=master")
// 	defer mssql.Close()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	mssql.Exec(queryDrop)
// 	mssql.Exec(queryCreate)
// 	return func() {
// 		mysql.Exec(queryDrop)
// 		postgres.Exec(queryDrop)
// 		mssql.Exec(queryDrop)
// 	}
// }

// // RunAll applies a sequence of unit tests to check the correct tracing of sql features.
// func RunAll(t *testing.T, cfg *Config) {
// 	cfg.mockTracer = mocktracer.Start()
// 	defer cfg.mockTracer.Stop()
// 	cfg.DB.SetMaxIdleConns(0)

// 	for name, test := range map[string]func(*Config) func(*testing.T){
// 		"Connect":       testConnect,
// 		"Ping":          testPing,
// 		"Query":         testQuery,
// 		"Statement":     testStatement,
// 		"BeginRollback": testBeginRollback,
// 		"Exec":          testExec,
// 	} {
// 		t.Run(name, test(cfg))
// 	}
// }

// func testConnect(cfg *Config) func(*testing.T) {
// 	return func(t *testing.T) {
// 		cfg.mockTracer.Reset()
// 		assert := assert.New(t)
// 		err := cfg.DB.Ping()
// 		assert.Nil(err)
// 		spans := cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 2)

// 		span := spans[0]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Connect"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func testPing(cfg *Config) func(*testing.T) {
// 	return func(t *testing.T) {
// 		cfg.mockTracer.Reset()
// 		assert := assert.New(t)
// 		err := cfg.DB.Ping()
// 		assert.Nil(err)
// 		spans := cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 2)

// 		verifyConnectSpan(spans[0], assert, cfg)

// 		span := spans[1]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Ping"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func testQuery(cfg *Config) func(*testing.T) {
// 	var query string
// 	switch cfg.DriverName {
// 	case "postgres", "pgx", "mysql":
// 		query = fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", cfg.TableName)
// 	case "sqlserver":
// 		query = fmt.Sprintf("SELECT TOP 5 id, name FROM %s", cfg.TableName)
// 	}
// 	return func(t *testing.T) {
// 		cfg.mockTracer.Reset()
// 		assert := assert.New(t)
// 		rows, err := cfg.DB.Query(query)
// 		defer rows.Close()
// 		assert.Nil(err)

// 		spans := cfg.mockTracer.FinishedSpans()
// 		var querySpan mocktracer.Span
// 		if cfg.DriverName == "sqlserver" {
// 			//The mssql driver doesn't support non-prepared queries so there are 3 spans
// 			//connect, prepare, and query
// 			assert.Len(spans, 3)
// 			span := spans[1]
// 			cfg.ExpectTags["sql.query_type"] = "Prepare"
// 			assert.Equal(cfg.ExpectName, span.OperationName())
// 			for k, v := range cfg.ExpectTags {
// 				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 			}
// 			querySpan = spans[2]

// 		} else {
// 			assert.Len(spans, 2)
// 			querySpan = spans[1]
// 		}

// 		verifyConnectSpan(spans[0], assert, cfg)
// 		cfg.ExpectTags["sql.query_type"] = "Query"
// 		assert.Equal(cfg.ExpectName, querySpan.OperationName())
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, querySpan.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func testStatement(cfg *Config) func(*testing.T) {
// 	query := "INSERT INTO %s(name) VALUES(%s)"
// 	switch cfg.DriverName {
// 	case "postgres", "pgx":
// 		query = fmt.Sprintf(query, cfg.TableName, "$1")
// 	case "mysql":
// 		query = fmt.Sprintf(query, cfg.TableName, "?")
// 	case "sqlserver":
// 		query = fmt.Sprintf(query, cfg.TableName, "@p1")
// 	}
// 	return func(t *testing.T) {
// 		cfg.mockTracer.Reset()
// 		assert := assert.New(t)
// 		stmt, err := cfg.DB.Prepare(query)
// 		assert.Equal(nil, err)

// 		spans := cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 3)

// 		verifyConnectSpan(spans[0], assert, cfg)

// 		span := spans[1]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Prepare"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}

// 		cfg.mockTracer.Reset()
// 		_, err2 := stmt.Exec("New York")
// 		assert.Equal(nil, err2)

// 		spans = cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 4)
// 		span = spans[2]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Exec"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func testBeginRollback(cfg *Config) func(*testing.T) {
// 	return func(t *testing.T) {
// 		cfg.mockTracer.Reset()
// 		assert := assert.New(t)

// 		tx, err := cfg.DB.Begin()
// 		assert.Equal(nil, err)

// 		spans := cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 2)

// 		verifyConnectSpan(spans[0], assert, cfg)

// 		span := spans[1]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Begin"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}

// 		cfg.mockTracer.Reset()
// 		err = tx.Rollback()
// 		assert.Equal(nil, err)

// 		spans = cfg.mockTracer.FinishedSpans()
// 		assert.Len(spans, 1)
// 		span = spans[0]
// 		assert.Equal(cfg.ExpectName, span.OperationName())
// 		cfg.ExpectTags["sql.query_type"] = "Rollback"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func testExec(cfg *Config) func(*testing.T) {
// 	return func(t *testing.T) {
// 		assert := assert.New(t)
// 		query := fmt.Sprintf("INSERT INTO %s(name) VALUES('New York')", cfg.TableName)

// 		parent, ctx := tracer.StartSpanFromContext(context.Background(), "test.parent",
// 			tracer.ServiceName("test"),
// 			tracer.ResourceName("parent"),
// 		)

// 		cfg.mockTracer.Reset()
// 		tx, err := cfg.DB.BeginTx(ctx, nil)
// 		assert.Equal(nil, err)
// 		_, err = tx.ExecContext(ctx, query)
// 		assert.Equal(nil, err)
// 		err = tx.Commit()
// 		assert.Equal(nil, err)

// 		parent.Finish() // flush children

// 		spans := cfg.mockTracer.FinishedSpans()
// 		if cfg.DriverName == "sqlserver" {
// 			//The mssql driver doesn't support non-prepared exec so there are 2 extra spans for the exec:
// 			//prepare, exec, and then a close
// 			assert.Len(spans, 7)
// 			span := spans[2]
// 			cfg.ExpectTags["sql.query_type"] = "Prepare"
// 			assert.Equal(cfg.ExpectName, span.OperationName())
// 			for k, v := range cfg.ExpectTags {
// 				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 			}
// 			span = spans[4]
// 			cfg.ExpectTags["sql.query_type"] = "Close"
// 			assert.Equal(cfg.ExpectName, span.OperationName())
// 			for k, v := range cfg.ExpectTags {
// 				assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 			}
// 		} else {
// 			assert.Len(spans, 5)
// 		}

// 		var span mocktracer.Span
// 		for _, s := range spans {
// 			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == query {
// 				span = s
// 			}
// 		}
// 		assert.NotNil(span, "span not found")
// 		cfg.ExpectTags["sql.query_type"] = "Exec"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 		for _, s := range spans {
// 			if s.OperationName() == cfg.ExpectName && s.Tag(ext.ResourceName) == "Commit" {
// 				span = s
// 			}
// 		}
// 		assert.NotNil(span, "span not found")
// 		cfg.ExpectTags["sql.query_type"] = "Commit"
// 		for k, v := range cfg.ExpectTags {
// 			assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 		}
// 	}
// }

// func verifyConnectSpan(span mocktracer.Span, assert *assert.Assertions, cfg *Config) {
// 	assert.Equal(cfg.ExpectName, span.OperationName())
// 	cfg.ExpectTags["sql.query_type"] = "Connect"
// 	for k, v := range cfg.ExpectTags {
// 		assert.Equal(v, span.Tag(k), "Value mismatch on tag %s", k)
// 	}
// }

// // Config holds the test configuration.
// type Config struct {
// 	*sql.DB
// 	mockTracer mocktracer.Tracer
// 	DriverName string
// 	TableName  string
// 	ExpectName string
// 	ExpectTags map[string]interface{}
// }

// // tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
// const tableName = "testsql"

// func TestMain(m *testing.M) {
// 	_, ok := os.LookupEnv("INTEGRATION")
// 	if !ok {
// 		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
// 		os.Exit(0)
// 	}
// 	defer sqltest.Prepare(tableName)()
// 	os.Exit(m.Run())
// }

// func TestSqlServer(t *testing.T) {
// 	driverName := "sqlserver"
// 	Register(driverName, &mssql.Driver{})
// 	defer unregister(driverName)
// 	db, err := Open(driverName, "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master")
// 	require.NoError(t, err)
// 	defer db.Close()

// 	testConfig := &sqltest.Config{
// 		DB:         db,
// 		DriverName: driverName,
// 		TableName:  tableName,
// 		ExpectName: "sqlserver.query",
// 		ExpectTags: map[string]interface{}{
// 			ext.ServiceName:     "sqlserver.db",
// 			ext.SpanType:        ext.SpanTypeSQL,
// 			ext.TargetHost:      "127.0.0.1",
// 			ext.TargetPort:      "1433",
// 			ext.DBUser:          "sa",
// 			ext.DBName:          "master",
// 			ext.EventSampleRate: nil,
// 			ext.DBSystem:        "mssql",
// 		},
// 	}
// 	sqltest.RunAll(t, testConfig)
// }

// func TestMySQL(t *testing.T) {
// 	driverName := "mysql"
// 	Register(driverName, &mysql.MySQLDriver{})
// 	defer unregister(driverName)
// 	db, err := Open(driverName, "test:test@tcp(127.0.0.1:3306)/test")
// 	require.NoError(t, err)
// 	defer db.Close()

// 	testConfig := &sqltest.Config{
// 		DB:         db,
// 		DriverName: driverName,
// 		TableName:  tableName,
// 		ExpectName: "mysql.query",
// 		ExpectTags: map[string]interface{}{
// 			ext.ServiceName:     "mysql.db",
// 			ext.SpanType:        ext.SpanTypeSQL,
// 			ext.TargetHost:      "127.0.0.1",
// 			ext.TargetPort:      "3306",
// 			ext.DBUser:          "test",
// 			ext.DBName:          "test",
// 			ext.EventSampleRate: nil,
// 			ext.DBSystem:        "mysql",
// 		},
// 	}
// 	sqltest.RunAll(t, testConfig)
// }

// func TestPostgres(t *testing.T) {
// 	driverName := "postgres"
// 	Register(driverName, &pq.Driver{}, WithServiceName("postgres-test"), WithAnalyticsRate(0.2))
// 	defer unregister(driverName)
// 	db, err := Open(driverName, "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
// 	require.NoError(t, err)
// 	defer db.Close()

// 	testConfig := &sqltest.Config{
// 		DB:         db,
// 		DriverName: driverName,
// 		TableName:  tableName,
// 		ExpectName: "postgres.query",
// 		ExpectTags: map[string]interface{}{
// 			ext.ServiceName:     "postgres-test",
// 			ext.SpanType:        ext.SpanTypeSQL,
// 			ext.TargetHost:      "127.0.0.1",
// 			ext.TargetPort:      "5432",
// 			ext.DBUser:          "postgres",
// 			ext.DBName:          "postgres",
// 			ext.EventSampleRate: 0.2,
// 			ext.DBSystem:        "postgresql",
// 		},
// 	}
// 	sqltest.RunAll(t, testConfig)
// }