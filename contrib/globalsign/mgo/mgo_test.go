package mgo

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
