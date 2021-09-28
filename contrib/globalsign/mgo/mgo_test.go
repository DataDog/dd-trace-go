// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/assert"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/globalconfig"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testMongoCollectionCommand(assert *assert.Assertions, command func(*Collection)) []mocktracer.Span {
	mt := mocktracer.Start()
	defer mt.Stop()

	parentSpan, ctx := tracer.StartSpanFromContext(
		context.Background(),
		"mgo-unittest",
		tracer.SpanType("app"),
		tracer.ResourceName("insert-test"),
	)

	session, err := Dial("localhost:27017", WithServiceName("unit-tests"), WithContext(ctx))
	defer session.Close()

	assert.NotNil(session)
	assert.Nil(err)

	db := session.DB("my_db")
	collection := db.C("MyCollection")

	command(collection)

	parentSpan.Finish()

	spans := mt.FinishedSpans()
	return spans
}

func TestCollection_Insert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
}

func TestCollection_Update(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Update(entity, entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
}

func TestCollection_UpdateId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpdateId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(5, len(spans))
	assert.Equal("mongodb.query", spans[3].OperationName())
}

func TestIssue874(t *testing.T) {
	// regression test for DataDog/dd-trace-go#873
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).All(&r)
		collection.Find(entity).Apply(mgo.Change{Update: entity}, &r)
		collection.Find(entity).Count()
		collection.Find(entity).Distinct("index", &r)
		collection.Find(entity).Explain(&r)
		collection.Find(entity).One(&r)
		collection.UpdateId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(9, len(spans))
}

func TestCollection_Upsert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Upsert(entity, entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpsertId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(6, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb.query", spans[4].OperationName())
}

func TestCollection_UpdateAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.UpdateAll(entity, entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
}

func TestCollection_FindId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		var r2 bson.D
		collection.FindId(r.Map()["_id"]).Iter().Next(&r2)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(6, len(spans))
}

func TestCollection_Remove(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Remove(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
}

func TestCollection_RemoveId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	removeByID := func(collection *Collection) {
		collection.Insert(entity)
		query := collection.Find(entity)
		iter := query.Iter()
		var r bson.D
		iter.Next(&r)
		id := r.Map()["_id"]
		err := collection.RemoveId(id)
		assert.NoError(err)
	}

	spans := testMongoCollectionCommand(assert, removeByID)
	assert.Equal(5, len(spans))
	assert.Equal("mongodb.query", spans[3].OperationName())
}

func TestCollection_RemoveAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.RemoveAll(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
}

func TestCollection_DropCollection(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.DropCollection()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
}

func TestCollection_Create(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.Create(&mgo.CollectionInfo{})
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
}

func TestCollection_Count(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.Count()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
}

func TestCollection_IndexCommands(t *testing.T) {
	assert := assert.New(t)

	indexTest := func(collection *Collection) {
		indexes, _ := collection.Indexes()
		collection.DropIndex("_id_")
		collection.DropIndexName("_id_")
		collection.EnsureIndex(indexes[0])
		collection.EnsureIndexKey("_id_")
	}

	spans := testMongoCollectionCommand(assert, indexTest)
	assert.Equal(6, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb.query", spans[2].OperationName())
	assert.Equal("mongodb.query", spans[3].OperationName())
	assert.Equal("mongodb.query", spans[4].OperationName())
	assert.Equal("mgo-unittest", spans[5].OperationName())
}

func TestCollection_FindAndIter(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Insert(entity)
		collection.Insert(entity)

		query := collection.Find(nil)
		iter := query.Iter()
		var r bson.D
		iter.Next(&r)
		var all []bson.D
		iter.All(&all)
		iter.Close()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(8, len(spans))
	assert.Equal("mongodb.query", spans[3].OperationName())
	assert.Equal("mongodb.query", spans[4].OperationName())
	assert.Equal("mongodb.query", spans[5].OperationName())
	assert.Equal("mongodb.query", spans[6].OperationName())
}

func TestCollection_Bulk(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *Collection) {
		bulk := collection.Bulk()
		bulk.Insert(entity)
		bulk.Run()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
}

func TestBadDial(t *testing.T) {
	assert.NotPanics(t, func() { Dial("this_is_not_valid?foo&bar") })
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...DialOption) {
		assert := assert.New(t)

		session, err := Dial("localhost:27017", opts...)
		assert.NoError(err)
		defer session.Close()

		db := session.DB("my_db")
		collection := db.C("MyCollection")
		bulk := collection.Bulk()
		bulk.Insert(bson.D{
			bson.DocElem{
				Name: "entity",
				Value: bson.DocElem{
					Name:  "index",
					Value: 0,
				},
			},
		})
		bulk.Run()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		s := spans[0]
		assert.Equal(rate, s.Tag(ext.EventSampleRate))
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
