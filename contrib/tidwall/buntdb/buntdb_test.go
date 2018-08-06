package buntdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/buntdb"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
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
		err := tx.AscendKeys("*", func(key, value string) bool {
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
		err := tx.DescendKeys("*", func(key, value string) bool {
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

func getDatabase(t *testing.T) *DB {
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

	return WrapDB(bdb)
}
