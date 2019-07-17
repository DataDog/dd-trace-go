// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package buntdb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/buntdb"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestAscend(t *testing.T) {
	testView(t, "Ascend", func(tx *Tx) error {
		var arr []string
		err := tx.Ascend("test-index", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:a", "1",
			"regular:b", "2",
			"regular:c", "3",
			"regular:d", "4",
			"regular:e", "5",
		}, arr)
		return nil
	})
}

func TestAscendEqual(t *testing.T) {
	testView(t, "AscendEqual", func(tx *Tx) error {
		var arr []string
		err := tx.AscendEqual("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{"regular:c", "3"}, arr)
		return nil
	})
}

func TestAscendGreaterOrEqual(t *testing.T) {
	testView(t, "AscendGreaterOrEqual", func(tx *Tx) error {
		var arr []string
		err := tx.AscendGreaterOrEqual("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:c", "3",
			"regular:d", "4",
			"regular:e", "5",
		}, arr)
		return nil
	})
}

func TestAscendKeys(t *testing.T) {
	testView(t, "AscendKeys", func(tx *Tx) error {
		var arr []string
		err := tx.AscendKeys("regular:*", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:a", "1",
			"regular:b", "2",
			"regular:c", "3",
			"regular:d", "4",
			"regular:e", "5",
		}, arr)
		return nil
	})
}

func TestAscendLessThan(t *testing.T) {
	testView(t, "AscendLessThan", func(tx *Tx) error {
		var arr []string
		err := tx.AscendLessThan("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:a", "1",
			"regular:b", "2",
		}, arr)
		return nil
	})
}

func TestAscendRange(t *testing.T) {
	testView(t, "AscendRange", func(tx *Tx) error {
		var arr []string
		err := tx.AscendRange("test-index", "2", "4", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:b", "2",
			"regular:c", "3",
		}, arr)
		return nil
	})
}

func TestCreateIndex(t *testing.T) {
	testUpdate(t, "CreateIndex", func(tx *Tx) error {
		err := tx.CreateIndex("test-create-index", "*")
		assert.NoError(t, err)
		return nil
	})
}

func TestCreateIndexOptions(t *testing.T) {
	testUpdate(t, "CreateIndexOptions", func(tx *Tx) error {
		err := tx.CreateIndexOptions("test-create-index", "*", nil)
		assert.NoError(t, err)
		return nil
	})
}

func TestCreateSpatialIndex(t *testing.T) {
	testUpdate(t, "CreateSpatialIndex", func(tx *Tx) error {
		err := tx.CreateSpatialIndex("test-create-index", "*", buntdb.IndexRect)
		assert.NoError(t, err)
		return nil
	})
}

func TestCreateSpatialIndexOptions(t *testing.T) {
	testUpdate(t, "CreateSpatialIndexOptions", func(tx *Tx) error {
		err := tx.CreateSpatialIndexOptions("test-create-index", "*", nil, buntdb.IndexRect)
		assert.NoError(t, err)
		return nil
	})
}

func TestDelete(t *testing.T) {
	testUpdate(t, "Delete", func(tx *Tx) error {
		val, err := tx.Delete("regular:a")
		assert.NoError(t, err)
		assert.Equal(t, "1", val)
		return nil
	})
}

func TestDeleteAll(t *testing.T) {
	testUpdate(t, "DeleteAll", func(tx *Tx) error {
		err := tx.DeleteAll()
		assert.NoError(t, err)
		return nil
	})
}

func TestDescend(t *testing.T) {
	testView(t, "Descend", func(tx *Tx) error {
		var arr []string
		err := tx.Descend("test-index", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:e", "5",
			"regular:d", "4",
			"regular:c", "3",
			"regular:b", "2",
			"regular:a", "1",
		}, arr)
		return nil
	})
}

func TestDescendEqual(t *testing.T) {
	testView(t, "DescendEqual", func(tx *Tx) error {
		var arr []string
		err := tx.DescendEqual("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{"regular:c", "3"}, arr)
		return nil
	})
}

func TestDescendGreaterThan(t *testing.T) {
	testView(t, "DescendGreaterThan", func(tx *Tx) error {
		var arr []string
		err := tx.DescendGreaterThan("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:e", "5",
			"regular:d", "4",
		}, arr)
		return nil
	})
}

func TestDescendKeys(t *testing.T) {
	testView(t, "DescendKeys", func(tx *Tx) error {
		var arr []string
		err := tx.DescendKeys("regular:*", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:e", "5",
			"regular:d", "4",
			"regular:c", "3",
			"regular:b", "2",
			"regular:a", "1",
		}, arr)
		return nil
	})
}

func TestDescendLessOrEqual(t *testing.T) {
	testView(t, "DescendLessOrEqual", func(tx *Tx) error {
		var arr []string
		err := tx.DescendLessOrEqual("test-index", "3", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:c", "3",
			"regular:b", "2",
			"regular:a", "1",
		}, arr)
		return nil
	})
}

func TestDescendRange(t *testing.T) {
	testView(t, "DescendRange", func(tx *Tx) error {
		var arr []string
		err := tx.DescendRange("test-index", "4", "2", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"regular:d", "4",
			"regular:c", "3",
		}, arr)
		return nil
	})
}

func TestDropIndex(t *testing.T) {
	testUpdate(t, "DropIndex", func(tx *Tx) error {
		err := tx.DropIndex("test-index")
		assert.NoError(t, err)
		return nil
	})
}

func TestGet(t *testing.T) {
	testView(t, "Get", func(tx *Tx) error {
		val, err := tx.Get("regular:a")
		assert.NoError(t, err)
		assert.Equal(t, "1", val)
		return nil
	})
}

func TestIndexes(t *testing.T) {
	testView(t, "Indexes", func(tx *Tx) error {
		indexes, err := tx.Indexes()
		assert.NoError(t, err)
		assert.Equal(t, []string{"test-index", "test-spatial-index"}, indexes)
		return nil
	})
}

func TestIntersects(t *testing.T) {
	testView(t, "Intersects", func(tx *Tx) error {
		var arr []string
		err := tx.Intersects("test-spatial-index", "[3 3],[4 4]", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"spatial:c", "[3 3]",
			"spatial:d", "[4 4]",
		}, arr)
		return nil
	})
}

func TestLen(t *testing.T) {
	testView(t, "Len", func(tx *Tx) error {
		n, err := tx.Len()
		assert.NoError(t, err)
		assert.Equal(t, 10, n)
		return nil
	})
}

func TestNearby(t *testing.T) {
	testView(t, "Nearby", func(tx *Tx) error {
		var arr []string
		err := tx.Nearby("test-spatial-index", "[3 3]", func(key, value string, distance float64) bool {
			arr = append(arr, key, value)
			return false
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"spatial:c", "[3 3]",
		}, arr)
		return nil
	})
}

func TestSet(t *testing.T) {
	testUpdate(t, "Set", func(tx *Tx) error {
		previousValue, replaced, err := tx.Set("regular:a", "11", nil)
		assert.NoError(t, err)
		assert.True(t, replaced)
		assert.Equal(t, "1", previousValue)
		return nil
	})
}

func TestTTL(t *testing.T) {
	testUpdate(t, "TTL", func(tx *Tx) error {
		duration, err := tx.TTL("regular:a")
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(-1), duration)
		return nil
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		db := getDatabase(t, opts...)
		defer db.Close()

		err := db.View(func(tx *Tx) error {
			_, err := tx.Len()
			return err
		})
		assert.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
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

func testUpdate(t *testing.T, name string, f func(tx *Tx) error) {
	mt := mocktracer.Start()
	defer mt.Stop()

	db := getDatabase(t)
	defer db.Close()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	err := db.WithContext(ctx).Update(f)
	assert.NoError(t, err)
	span.Finish()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	assert.Equal(t, ext.AppTypeDB, spans[0].Tag(ext.SpanType))
	assert.Equal(t, name, spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "buntdb", spans[0].Tag(ext.ServiceName))
	assert.Equal(t, "buntdb.query", spans[0].OperationName())
}

func testView(t *testing.T, name string, f func(tx *Tx) error) {
	mt := mocktracer.Start()
	defer mt.Stop()

	db := getDatabase(t)
	defer db.Close()

	err := db.View(f)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, ext.AppTypeDB, spans[0].Tag(ext.SpanType))
	assert.Equal(t, name, spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "buntdb", spans[0].Tag(ext.ServiceName))
	assert.Equal(t, "buntdb.query", spans[0].OperationName())
}

func getDatabase(t *testing.T, opts ...Option) *DB {
	bdb, err := buntdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.CreateIndex("test-index", "regular:*", buntdb.IndexBinary)
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.CreateSpatialIndex("test-spatial-index", "spatial:*", buntdb.IndexRect)
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.Update(func(tx *buntdb.Tx) error {
		tx.Set("regular:a", "1", nil)
		tx.Set("regular:b", "2", nil)
		tx.Set("regular:c", "3", nil)
		tx.Set("regular:d", "4", nil)
		tx.Set("regular:e", "5", nil)

		tx.Set("spatial:a", "[1 1]", nil)
		tx.Set("spatial:b", "[2 2]", nil)
		tx.Set("spatial:c", "[3 3]", nil)
		tx.Set("spatial:d", "[4 4]", nil)
		tx.Set("spatial:e", "[5 5]", nil)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return WrapDB(bdb, opts...)
}
