// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/lib/pq"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const (
	tableName           = "testgorm"
	pgConnString        = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	sqlServerConnString = "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
	mysqlConnString     = "test:test@tcp(127.0.0.1:3306)/test"
)

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

func TestOpenDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			assert.Fail(t, "Opening connection panicked", r)
		}
	}()
	// None of the following calls should end up in a panic.
	_, _ = gorm.Open(nil, &gorm.Config{})
	_, _ = gorm.Open(nil, nil)
	_, _ = Open(nil, &gorm.Config{})
	_, _ = Open(nil, nil)
}

func TestMySQL(t *testing.T) {
	sqltrace.Register("mysql", &mysql.MySQLDriver{}, sqltrace.WithServiceName("mysql-test"))
	sqlDb, err := sqltrace.Open("mysql", mysqlConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(mysqlgorm.New(mysqlgorm.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	internalDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}

	testConfig := &sqltest.Config{
		DB:         internalDB,
		DriverName: "mysql",
		TableName:  tableName,
		ExpectName: "mysql.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "mysql-test",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "3306",
			ext.DBUser:      "test",
			ext.DBName:      "test",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestPostgres(t *testing.T) {
	sqltrace.Register("pgx", &stdlib.Driver{})
	sqlDb, err := sqltrace.Open("pgx", pgConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	internalDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}

	testConfig := &sqltest.Config{
		DB:         internalDB,
		DriverName: "pgx",
		TableName:  tableName,
		ExpectName: "pgx.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "pgx.db",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "5432",
			ext.DBUser:      "postgres",
			ext.DBName:      "postgres",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestSQLServer(t *testing.T) {
	sqltrace.Register("sqlserver", &mssql.Driver{})
	sqlDb, err := sqltrace.Open("sqlserver", sqlServerConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(sqlserver.New(sqlserver.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	internalDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}

	testConfig := &sqltest.Config{
		DB:         internalDB,
		DriverName: "sqlserver",
		TableName:  tableName,
		ExpectName: "sqlserver.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "sqlserver.db",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "1433",
			ext.DBUser:      "sa",
			ext.DBName:      "master",
		},
	}
	sqltest.RunAll(t, testConfig)
}

type Product struct {
	gorm.Model
	Code  string
	Price uint
}

func TestCallbacks(t *testing.T) {
	sqltrace.Register("pgx", &stdlib.Driver{})
	sqlDb, err := sqltrace.Open("pgx", pgConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	err = db.AutoMigrate(&Product{})
	if err != nil {
		log.Fatal(err)
	}

	t.Run("create", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		var queryText string
		db.Callback().Create().After("testing").Register("query text", func(d *gorm.DB) {
			queryText = d.Statement.SQL.String()
		})
		db.Create(&Product{Code: "L1212", Price: 1000})

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		a.Equal("gorm.create", span.OperationName())
		a.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		a.Equal(queryText, span.Tag(ext.ResourceName))
		a.Equal("gorm.io/gorm.v1", span.Tag(ext.Component))
		a.Equal(parentSpan.Context().SpanID(), span.ParentID())

		for _, s := range spans {
			if s.Tag(ext.Component) == "jackc/pgx.v5" {
				// The underlying driver should receive the gorm span
				a.Equal(span.SpanID(), s.ParentID())
			}
		}
	})

	t.Run("query", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		var queryText string
		db.Callback().Query().After("testing").Register("query text", func(d *gorm.DB) {
			queryText = d.Statement.SQL.String()
		})
		var product Product
		db.First(&product, "code = ?", "L1212")

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		a.Equal("gorm.query", span.OperationName())
		a.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		a.Equal(queryText, span.Tag(ext.ResourceName))
		a.Equal("gorm.io/gorm.v1", span.Tag(ext.Component))
		a.Equal(parentSpan.Context().SpanID(), span.ParentID())

		for _, s := range spans {
			if s.Tag(ext.Component) == "jackc/pgx.v5" {
				// The underlying driver should receive the gorm span
				a.Equal(span.SpanID(), s.ParentID())
			}
		}
	})

	t.Run("dry_run", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		db.DryRun = true
		var product Product
		db.First(&product, "code = ?", "L1212")

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.Len(spans, 1) // No additional span generated
	})

	t.Run("update", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		var queryText string
		db.Callback().Update().After("testing").Register("query text", func(d *gorm.DB) {
			queryText = d.Statement.SQL.String()
		})
		var product Product
		db.First(&product, "code = ?", "L1212")
		db.Model(&product).Update("Price", 2000)

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		a.Equal("gorm.update", span.OperationName())
		a.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		a.Equal(queryText, span.Tag(ext.ResourceName))
		a.Equal("gorm.io/gorm.v1", span.Tag(ext.Component))
		a.Equal(parentSpan.Context().SpanID(), span.ParentID())

		for _, s := range spans {
			if s.Tag(ext.Component) == "jackc/pgx.v5" {
				// The underlying driver should receive the gorm span
				a.Equal(span.SpanID(), s.ParentID())
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		var queryText string
		db.Callback().Delete().After("testing").Register("query text", func(d *gorm.DB) {
			queryText = d.Statement.SQL.String()
		})
		var product Product
		db.First(&product, "code = ?", "L1212")
		db.Delete(&product)

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		a.Equal("gorm.delete", span.OperationName())
		a.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		a.Equal(queryText, span.Tag(ext.ResourceName))
		a.Equal("gorm.io/gorm.v1", span.Tag(ext.Component))
		a.Equal(parentSpan.Context().SpanID(), span.ParentID())

		for _, s := range spans {
			if s.Tag(ext.Component) == "jackc/pgx.v5" {
				// The underlying driver should receive the gorm span
				a.Equal(span.SpanID(), s.ParentID())
			}
		}
	})

	t.Run("raw", func(t *testing.T) {
		a := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db := db.WithContext(ctx)
		var queryText string
		db.Callback().Raw().After("testing").Register("query text", func(d *gorm.DB) {
			queryText = d.Statement.SQL.String()
		})

		err := db.Exec("select 1").Error
		assert.Nil(t, err)

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		a.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		a.Equal("gorm.raw_query", span.OperationName())
		a.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		a.Equal(queryText, span.Tag(ext.ResourceName))

		for _, s := range spans {
			if s.Tag(ext.Component) == "jackc/pgx.v5" {
				// The underlying driver should receive the gorm span
				a.Equal(span.SpanID(), s.ParentID())
			}
		}
	})
}

func TestAnalyticsSettings(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	sqltrace.Register("pgx", &stdlib.Driver{})
	sqlDb, err := sqltrace.Open("pgx", pgConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	err = db.AutoMigrate(&Product{})
	if err != nil {
		log.Fatal(err)
	}

	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{}, opts...)
		if err != nil {
			log.Fatal(err)
		}

		db = db.WithContext(ctx)
		db.Create(&Product{Code: "L1212", Price: 1000})

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(t, len(spans) > 2)
		s := spans[len(spans)-2]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestContext(t *testing.T) {
	sqltrace.Register("pgx", &stdlib.Driver{})
	sqlDb, err := sqltrace.Open("pgx", pgConnString)
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	t.Run("with", func(t *testing.T) {
		const contextKey = "text context"

		type key string
		testCtx := context.WithValue(context.Background(), key(contextKey), true)
		db := db.WithContext(testCtx)
		ctx := db.Statement.Context
		assert.Equal(t, testCtx.Value(key(contextKey)), ctx.Value(key(contextKey)))
	})
}

func TestError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	assertErrCheck := func(t *testing.T, mt mocktracer.Tracer, errExist bool, opts ...Option) {
		sqltrace.Register("pgx", &stdlib.Driver{})
		sqlDb, err := sqltrace.Open("pgx", pgConnString)
		assert.Nil(t, err)

		db, err := Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{}, opts...)
		assert.Nil(t, err)
		db.AutoMigrate(&Product{})
		db.First(&Product{}, Product{Code: "L1210", Price: 2000})

		spans := mt.FinishedSpans()
		assert.True(t, len(spans) > 1)

		// Get last span (gorm.db)
		s := spans[len(spans)-1]

		assert.Equal(t, errExist, s.Tag(ext.Error) != nil)
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		assertErrCheck(t, mt, true)
	})

	t.Run("errcheck", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		errFn := func(err error) bool {
			return err != gorm.ErrRecordNotFound
		}
		assertErrCheck(t, mt, false, WithErrorCheck(errFn))
	})
}

func TestCustomTags(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	sqltrace.Register("pgx", &stdlib.Driver{}, sqltrace.WithChildSpansOnly())
	sqlDb, err := sqltrace.Open("pgx", pgConnString, sqltrace.WithChildSpansOnly())
	if err != nil {
		log.Fatal(err)
	}

	db, err := Open(
		postgres.New(postgres.Config{Conn: sqlDb}),
		&gorm.Config{},
		WithCustomTag("foo", func(_ *gorm.DB) interface{} {
			return "bar"
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	err = db.AutoMigrate(&Product{})
	if err != nil {
		log.Fatal(err)
	}

	db = db.WithContext(context.Background())
	db.First(&Product{}, Product{Code: "L1210", Price: 2000})

	spans := mt.FinishedSpans()
	assert.True(len(spans) > 0)

	// Get last span (gorm.db)
	s := spans[len(spans)-1]

	assert.Equal("bar", s.Tag("foo"))
}

func TestPlugin(t *testing.T) {
	db, err := gorm.Open(&tests.DummyDialector{})
	require.NoError(t, err)

	opt := WithCustomTag("foo", func(_ *gorm.DB) interface{} {
		return "bar"
	})
	plugin := NewTracePlugin(opt).(tracePlugin)

	assert.Equal(t, "DDTracePlugin", plugin.Name())
	assert.Len(t, plugin.options, 1)
	require.NoError(t, db.Use(plugin))

	assert.NotNil(t, db.Callback().Create().Get("dd-trace-go:before_create"))
	assert.NotNil(t, db.Callback().Create().Get("dd-trace-go:after_create"))

	assert.NotNil(t, db.Callback().Update().Get("dd-trace-go:before_update"))
	assert.NotNil(t, db.Callback().Update().Get("dd-trace-go:after_update"))

	assert.NotNil(t, db.Callback().Delete().Get("dd-trace-go:before_delete"))
	assert.NotNil(t, db.Callback().Delete().Get("dd-trace-go:after_delete"))

	assert.NotNil(t, db.Callback().Query().Get("dd-trace-go:before_query"))
	assert.NotNil(t, db.Callback().Query().Get("dd-trace-go:after_query"))

	assert.NotNil(t, db.Callback().Row().Get("dd-trace-go:before_row_query"))
	assert.NotNil(t, db.Callback().Row().Get("dd-trace-go:after_row_query"))

	assert.NotNil(t, db.Callback().Raw().Get("dd-trace-go:before_raw_query"))
	assert.NotNil(t, db.Callback().Raw().Get("dd-trace-go:before_raw_query"))
}
