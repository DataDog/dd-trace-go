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
	"log"
	"math"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const tableName = "testsql"

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	defer sqltest.Prepare(tableName)()
	os.Exit(m.Run())
}

func TestSqlServer(t *testing.T) {
	Register("sqlserver", &mssql.Driver{})
	db, err := Open("sqlserver", "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: "sqlserver",
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
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestMySQL(t *testing.T) {
	Register("mysql", &mysql.MySQLDriver{})
	db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: "mysql",
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
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestPostgres(t *testing.T) {
	Register("postgres", &pq.Driver{}, WithServiceName("postgres-test"), WithAnalyticsRate(0.2))
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db,
		DriverName: "postgres",
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
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestOpenOptions(t *testing.T) {
	Register("postgres", &pq.Driver{}, WithServiceName("postgres-test"), WithAnalyticsRate(0.2))

	t.Run("Open", func(t *testing.T) {
		db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
			WithServiceName("override-test"),
			WithAnalytics(true),
		)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: "postgres",
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
			},
		}
		sqltest.RunAll(t, testConfig)
	})

	t.Run("OpenDB", func(t *testing.T) {
		c, err := pq.NewConnector("postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
		if err != nil {
			log.Fatal(err)
		}
		db := OpenDB(c)
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: "postgres",
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
			},
		}
		sqltest.RunAll(t, testConfig)
	})

	t.Run("WithDSN", func(t *testing.T) {
		dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
		c, err := pq.NewConnector(dsn)
		if err != nil {
			log.Fatal(err)
		}
		db := OpenDB(c, WithDSN(dsn))
		defer db.Close()

		testConfig := &sqltest.Config{
			DB:         db,
			DriverName: "postgres",
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
			},
		}
		sqltest.RunAll(t, testConfig)
	})
}

func TestCommentInjectionModes(t *testing.T) {
	testCases := []struct {
		name                 string
		mode                 tracer.SQLCommentInjectionMode
		expectedInjectedTags sqltest.TagInjectionExpectation
	}{
		{
			name: "default (no injection)",
			expectedInjectedTags: sqltest.TagInjectionExpectation{
				StaticTags:  false,
				DynamicTags: false,
			},
		},
		{
			name: "static tags injection",
			mode: tracer.StaticTagsSQLCommentInjection,
			expectedInjectedTags: sqltest.TagInjectionExpectation{
				StaticTags:  true,
				DynamicTags: false,
			},
		},
		{
			name: "dynamic tags injection",
			mode: tracer.FullSQLCommentInjection,
			expectedInjectedTags: sqltest.TagInjectionExpectation{
				StaticTags:  true,
				DynamicTags: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// TODO: Rethink how to run that test now that the functionality is implemented in a propagator
			// that we can't currently inject on the mocktracer
			mockTracer := mocktracer.Start()
			defer mockTracer.Stop()

			Register("postgres", &pq.Driver{}, WithServiceName("postgres-test"))
			defer unregister("postgres")

			db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
			if err != nil {
				log.Fatal(err)
			}
			defer db.Close()

			testConfig := &sqltest.Config{
				DB:         db,
				DriverName: "postgres",
				TableName:  tableName,
				ExpectName: "postgres.query",
				ExpectTags: map[string]interface{}{
					ext.ServiceName: "postgres-test",
					ext.SpanType:    ext.SpanTypeSQL,
					ext.TargetHost:  "127.0.0.1",
					ext.TargetPort:  "5432",
					ext.DBUser:      "postgres",
					ext.DBName:      "postgres",
				},
				ExpectTagInjection: tc.expectedInjectedTags,
			}

			sqltest.RunAll(t, testConfig)
		})
	}
}

func TestMySQLUint64(t *testing.T) {
	Register("mysql", &mysql.MySQLDriver{})
	db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
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

func (h *hangingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, errors.New("context cancelled")
	}
}

func (h *hangingConnector) Driver() driver.Driver {
	panic("hangingConnector: Driver() not implemented")
}

func TestConnectCancelledCtx(t *testing.T) {
	mockTracer := mocktracer.Start()
	defer mockTracer.Stop()
	assert := assert.New(t)
	tc := tracedConnector{
		connector:  &hangingConnector{},
		driverName: "hangingConnector",
		cfg:        new(config),
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
