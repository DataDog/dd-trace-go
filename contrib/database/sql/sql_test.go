// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	sqltest "github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/sql"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const tableName = "testsql"

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	cleanup := sqltest.Prepare(tableName)
	testResult := m.Run()
	cleanup()
	os.Exit(testResult)
}

func TestSqlServer(t *testing.T) {
	driverName := "sqlserver"
	Register(driverName, &mssql.Driver{})
	defer unregister(driverName)
	db, err := Open(driverName, "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master")
	require.NoError(t, err)
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: driverName,
		TableName:  tableName,
		ExpectName: "sqlserver.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName:     "sqlserver.db",
			ext.SpanType:        ext.SpanTypeSQL,
			ext.TargetHost:      "127.0.0.1",
			ext.TargetPort:      "1433",
			ext.DBUser:          "sa",
			ext.DBName:          "master",
			ext.EventSampleRate: nil,
			ext.DBSystem:        "mssql",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestMySQL(t *testing.T) {
	driverName := "mysql"
	Register(driverName, &mysql.MySQLDriver{})
	defer unregister(driverName)
	db, err := Open(driverName, "test:test@tcp(127.0.0.1:3306)/test")
	require.NoError(t, err)
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: driverName,
		TableName:  tableName,
		ExpectName: "mysql.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName:     "mysql.db",
			ext.SpanType:        ext.SpanTypeSQL,
			ext.TargetHost:      "127.0.0.1",
			ext.TargetPort:      "3306",
			ext.DBUser:          "test",
			ext.DBName:          "test",
			ext.EventSampleRate: nil,
			ext.DBSystem:        "mysql",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestPostgres(t *testing.T) {
	driverName := "postgres"
	Register(driverName, &pq.Driver{}, WithService("postgres-test"), WithAnalyticsRate(0.2))
	defer unregister(driverName)
	db, err := Open(driverName, "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: driverName,
		TableName:  tableName,
		ExpectName: "postgres.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName:     "postgres-test",
			ext.SpanType:        ext.SpanTypeSQL,
			ext.TargetHost:      "127.0.0.1",
			ext.TargetPort:      "5432",
			ext.DBUser:          "postgres",
			ext.DBName:          "postgres",
			ext.EventSampleRate: 0.2,
			ext.DBSystem:        "postgresql",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestOpenOptions(t *testing.T) {
	driverName := "postgres"
	dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	// shorten `interval` for the `WithDBStats` test
	// interval must get reassigned outside of a subtest to avoid a data race
	interval = 500 * time.Millisecond
	t.Cleanup(func() {
		interval = 10 * time.Second // resetting back to original value
	})

	t.Run("Open", func(t *testing.T) {
		Register(driverName, &pq.Driver{}, WithService("postgres-test"), WithAnalyticsRate(0.2))
		defer unregister(driverName)
		db, err := Open(driverName, dsn, WithService("override-test"), WithAnalytics(true))
		require.NoError(t, err)
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: driverName,
			TableName:  tableName,
			ExpectName: "postgres.query",
			ExpectTags: map[string]interface{}{
				ext.ServiceName:     "override-test",
				ext.SpanType:        ext.SpanTypeSQL,
				ext.TargetHost:      "127.0.0.1",
				ext.TargetPort:      "5432",
				ext.DBUser:          "postgres",
				ext.DBName:          "postgres",
				ext.EventSampleRate: 1.0,
				ext.DBSystem:        "postgresql",
			},
		}
		sqltest.RunAll(t, testConfig)
	})

	t.Run("OpenDB", func(t *testing.T) {
		Register(driverName, &pq.Driver{}, WithService("postgres-test"), WithAnalyticsRate(0.2))
		defer unregister(driverName)
		c, err := pq.NewConnector(dsn)
		require.NoError(t, err)
		db := OpenDB(c)
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: driverName,
			TableName:  tableName,
			ExpectName: "postgres.query",
			ExpectTags: map[string]interface{}{
				ext.ServiceName:     "postgres-test",
				ext.SpanType:        ext.SpanTypeSQL,
				ext.TargetHost:      nil,
				ext.TargetPort:      nil,
				ext.DBUser:          nil,
				ext.DBName:          nil,
				ext.EventSampleRate: 0.2,
				ext.DBSystem:        "postgresql",
			},
		}
		sqltest.RunAll(t, testConfig)
	})

	t.Run("WithDSN", func(t *testing.T) {
		Register(driverName, &pq.Driver{}, WithService("postgres-test"), WithAnalyticsRate(0.2))
		defer unregister(driverName)
		c, err := pq.NewConnector(dsn)
		require.NoError(t, err)
		db := OpenDB(c, WithDSN(dsn))
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: driverName,
			TableName:  tableName,
			ExpectName: "postgres.query",
			ExpectTags: map[string]interface{}{
				ext.ServiceName:     "postgres-test",
				ext.SpanType:        ext.SpanTypeSQL,
				ext.TargetHost:      "127.0.0.1",
				ext.TargetPort:      "5432",
				ext.DBUser:          "postgres",
				ext.DBName:          "postgres",
				ext.EventSampleRate: 0.2,
				ext.DBSystem:        "postgresql",
			},
		}
		sqltest.RunAll(t, testConfig)
	})

	t.Run("WithChildSpansOnly", func(t *testing.T) {
		Register(driverName, &pq.Driver{})
		defer unregister(driverName)
		db, err := Open(driverName, dsn, WithChildSpansOnly())
		require.NoError(t, err)

		mt := mocktracer.Start()
		defer mt.Stop()

		err = db.Ping()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		// the number of spans should be 0 since we specified the WithChildSpansOnly option
		assert.Len(t, spans, 0)
	})

	t.Run("WithIgnoreQueryTypes", func(t *testing.T) {
		registerOpts := []Option{WithIgnoreQueryTypes(QueryTypeConnect)}
		openDBOpts := []Option{WithIgnoreQueryTypes(QueryTypeConnect, QueryTypePing)}
		Register(driverName, &pq.Driver{}, registerOpts...)
		defer unregister(driverName)
		db, err := Open(driverName, dsn, openDBOpts...)
		require.NoError(t, err)

		mt := mocktracer.Start()
		defer mt.Stop()

		err = db.Ping()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		// the number of spans should be 0 since we are ignoring Connect and Ping spans.
		assert.Len(t, spans, 0)
	})

	t.Run("RegisterOptionsAsDefault", func(t *testing.T) {
		registerOpts := []Option{
			WithService("register-override"),
			WithIgnoreQueryTypes(QueryTypeConnect),
		}
		Register(driverName, &pq.Driver{}, registerOpts...)
		defer unregister(driverName)
		db, err := Open(driverName, dsn)
		require.NoError(t, err)

		mt := mocktracer.Start()
		defer mt.Stop()

		err = db.Ping()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		// the number of spans should be 1 since we are ignoring Connect spans from the Register options.
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "register-override", s0.Tag(ext.ServiceName))
	})

	t.Run("WithDBStats", func(t *testing.T) {
		m := testutils.NewMockStatsdClient()
		Register(driverName, &pq.Driver{})
		defer unregister(driverName)
		db, err := Open(driverName, dsn, withStatsdClient(m), WithDBStats())
		require.NoError(t, err)

		// The polling interval has been reduced to 500ms for the sake of this test, so at least one round of `pollDBStats` should be complete in 1s
		deadline := time.Now().Add(1 * time.Second)
		wantStats := []string{MaxOpenConnections, OpenConnections, InUse, Idle, WaitCount, WaitDuration, MaxIdleClosed, MaxIdleTimeClosed, MaxLifetimeClosed}
		var calls1 []string
		for {
			if time.Now().After(deadline) {
				t.Fatalf("Stats not collected in expected interval of %v", interval)
			}
			calls := m.CallNames()
			// if the expected volume of stats has been collected, ensure 9/9 of the DB Stats are included
			if len(calls) >= len(wantStats) {
				for _, s := range wantStats {
					if !assert.Contains(t, calls, s) {
						t.Fatalf("Missing stat %s", s)
					}
				}
				// all expected stats have been collected; exit out of loop, test should pass
				calls1 = calls
				break
			}
			// not all stats have been collected yet, try again in 50ms
			time.Sleep(50 * time.Millisecond)
		}
		// Close DB and assert the no further stats have been collected; db.Close should stop the pollDBStats goroutine.
		db.Close()
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, calls1, m.CallNames())
	})
}

func withStatsdClient(s instrumentation.StatsdClient) OptionFn {
	return func(c *config) {
		c.statsdClient = s
	}
}

func TestMySQLUint64(t *testing.T) {
	Register("mysql", &mysql.MySQLDriver{})
	defer unregister("mysql")
	db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	require.NoError(t, err)
	defer db.Close()

	assert := assert.New(t)
	rows, err := db.Query("SELECT ?", uint64(math.MaxUint64))
	assert.NoError(err)
	assert.NotNil(rows)
	assert.True(rows.Next())
	var result uint64
	rows.Scan(&result)
	assert.Equal(uint64(math.MaxUint64), result)
	assert.False(rows.Next())
	assert.NoError(rows.Err())
	assert.NoError(rows.Close())
}

// hangingConnector hangs on Connect until ctx is cancelled.
type hangingConnector struct{}

func (h *hangingConnector) Open(_ string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (h *hangingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, errors.New("context cancelled")
	}
}

func (h *hangingConnector) Driver() driver.Driver {
	return h
}

func TestConnectCancelledCtx(t *testing.T) {
	mockTracer := mocktracer.Start()
	defer mockTracer.Stop()
	assert := assert.New(t)
	driverName := "hangingConnector"
	cfg := new(config)
	defaults(cfg, driverName, nil)
	tc := tracedConnector{
		connector:  &hangingConnector{},
		driverName: driverName,
		cfg:        cfg,
	}
	ctx, cancelFunc := context.WithCancel(context.Background())

	go func() {
		tc.Connect(ctx)
	}()
	time.Sleep(time.Millisecond * 100)
	cancelFunc()
	time.Sleep(time.Millisecond * 100)

	spans := mockTracer.FinishedSpans()
	assert.Len(spans, 1)
	s := spans[0]
	assert.Equal("hangingConnector.query", s.OperationName())
	assert.Equal("Connect", s.Tag("sql.query_type"))
}

func TestRegister(_ *testing.T) {
	var wg sync.WaitGroup

	for i := 1; i < 10; i++ {
		wg.Add(1)
		go func(i int64) {
			Register("test"+strconv.FormatInt(i, 10), &mysql.MySQLDriver{})
			wg.Done()
		}(int64(i))
	}

	wg.Wait()
	// cleanup registered drivers
	for i := 1; i < 10; i++ {
		unregister("test" + strconv.FormatInt(int64(i), 10))
	}
}
