// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

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
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"gopkg.in/jinzhu/gorm.v1"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const tableName = "testgorm"

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	defer sqltest.Prepare(tableName)()
	os.Exit(m.Run())
}

func TestMySQL(t *testing.T) {
	sqltrace.Register("mysql", &mysql.MySQLDriver{}, sqltrace.WithServiceName("mysql-test"))
	db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db.DB(),
		DriverName: "mysql",
		TableName:  tableName,
		ExpectName: "mysql.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "mysql-test",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "3306",
			"db.user":       "test",
			"db.name":       "test",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestPostgres(t *testing.T) {
	sqltrace.Register("postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testConfig := &sqltest.Config{
		DB:         db.DB(),
		DriverName: "postgres",
		TableName:  tableName,
		ExpectName: "postgres.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "postgres.db",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "5432",
			"db.user":       "postgres",
			"db.name":       "postgres",
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
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	sqltrace.Register("postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.AutoMigrate(&Product{})

	t.Run("create", func(t *testing.T) {
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db = WithContext(ctx, db)
		db.Create(&Product{Code: "L1212", Price: 1000})

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(len(spans) >= 3)

		span := spans[len(spans)-3]
		assert.Equal("gorm.create", span.OperationName())
		assert.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		assert.Equal(
			`INSERT INTO "products" ("created_at","updated_at","deleted_at","code","price") VALUES ($1,$2,$3,$4,$5) RETURNING "products"."id"`,
			span.Tag(ext.ResourceName))
	})

	t.Run("query", func(t *testing.T) {
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db = WithContext(ctx, db)
		var product Product
		db.First(&product, "code = ?", "L1212")

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(len(spans) >= 2)

		span := spans[len(spans)-2]
		assert.Equal("gorm.query", span.OperationName())
		assert.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		assert.Equal(
			`SELECT * FROM "products"  WHERE "products"."deleted_at" IS NULL AND ((code = $1)) ORDER BY "products"."id" ASC LIMIT 1`,
			span.Tag(ext.ResourceName))
	})

	t.Run("update", func(t *testing.T) {
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db = WithContext(ctx, db)
		var product Product
		db.First(&product, "code = ?", "L1212")
		db.Model(&product).Update("Price", 2000)

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(len(spans) >= 3)

		span := spans[len(spans)-3]
		assert.Equal("gorm.update", span.OperationName())
		assert.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		assert.Equal(
			`UPDATE "products" SET "price" = $1, "updated_at" = $2  WHERE "products"."deleted_at" IS NULL AND "products"."id" = $3`,
			span.Tag(ext.ResourceName))
	})

	t.Run("delete", func(t *testing.T) {
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db = WithContext(ctx, db)
		var product Product
		db.First(&product, "code = ?", "L1212")
		db.Delete(&product)

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(len(spans) >= 3)

		span := spans[len(spans)-3]
		assert.Equal("gorm.delete", span.OperationName())
		assert.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
		assert.Equal(
			`UPDATE "products" SET "deleted_at"=$1  WHERE "products"."deleted_at" IS NULL AND "products"."id" = $2`,
			span.Tag(ext.ResourceName))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	sqltrace.Register("postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.AutoMigrate(&Product{})

	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable", opts...)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		db.AutoMigrate(&Product{})

		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		db = WithContext(ctx, db)
		db.Create(&Product{Code: "L1212", Price: 1000})

		parentSpan.Finish()

		spans := mt.FinishedSpans()
		assert.True(t, len(spans) > 3)
		s := spans[len(spans)-3]
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
	sqltrace.Register("postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	t.Run("with", func(t *testing.T) {
		type key string
		testCtx := context.WithValue(context.Background(), key("test context"), true)
		db := WithContext(testCtx, db)
		ctx := ContextFromDB(db)
		assert.Equal(t, testCtx, ctx)
	})

	t.Run("without", func(t *testing.T) {
		ctx := ContextFromDB(db)
		assert.Equal(t, context.Background(), ctx)
	})
}

func TestCustomTags(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	sqltrace.Register("postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
		WithCustomTag("custom_tag", func(scope *gorm.Scope) interface{} {
			return scope.SQLVars[3]
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.AutoMigrate(&Product{})

	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
		tracer.ServiceName("fake-http-server"),
		tracer.SpanType(ext.SpanTypeWeb),
	)

	db = WithContext(ctx, db)
	db.Create(&Product{Code: "L1212", Price: 1000})

	parentSpan.Finish()

	spans := mt.FinishedSpans()
	assert.True(len(spans) >= 3)

	// We deterministically expect the span to be the third last,
	// followed by the underlying postgres DB trace and the above http.request span.
	span := spans[len(spans)-3]
	assert.Equal("gorm.create", span.OperationName())
	assert.Equal(ext.SpanTypeSQL, span.Tag(ext.SpanType))
	assert.Equal("L1212", span.Tag("custom_tag"))
	assert.Equal(
		`INSERT INTO "products" ("created_at","updated_at","deleted_at","code","price") VALUES ($1,$2,$3,$4,$5) RETURNING "products"."id"`,
		span.Tag(ext.ResourceName))
}

func TestError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	assertErrCheck := func(t *testing.T, mt mocktracer.Tracer, errExist bool, opts ...Option) {
		sqltrace.Register("postgres", &pq.Driver{})
		db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable", opts...)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		db.AutoMigrate(&Product{})

		db = WithContext(context.Background(), db)
		db.Find(&Product{}, Product{Code: "L1212", Price: 1000})

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
			if err == gorm.ErrRecordNotFound {
				return false
			}
			return true
		}
		assertErrCheck(t, mt, false, WithErrorCheck(errFn))
	})
}
