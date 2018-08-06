package mgo

import (
	"context"
	"testing"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func testMongoCollectionCommand(assert *assert.Assertions, command func(*WrapCollection)) []mocktracer.Span {
	mt := mocktracer.Start()
	defer mt.Stop()

	parentSpan, ctx := tracer.StartSpanFromContext(
		context.Background(),
		"mgo-unittest",
		tracer.SpanType("app"),
		tracer.ResourceName("insert-test"),
	)

	session, err := Dial(ctx, "192.168.33.10:27017", WithServiceName("unit-tests"))
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

func TestWrapCollection_Insert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.insert", spans[0].OperationName())
}

func TestWrapCollection_Update(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		collection.Update(entity, entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.update", spans[1].OperationName())
}

func TestWrapCollection_UpdateId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpdateId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(5, len(spans))
	assert.Equal("mongodb.updateid", spans[3].OperationName())
}

func TestWrapCollection_Upsert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		collection.Upsert(entity, entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpsertId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(6, len(spans))
	assert.Equal("mongodb.upsert", spans[1].OperationName())
	assert.Equal("mongodb.upsertid", spans[4].OperationName())
}

func TestWrapCollection_UpdateAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		collection.UpdateAll(entity, entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.updateall", spans[1].OperationName())
}

func TestWrapCollection_FindId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		var r2 bson.D
		collection.FindId(r.Map()["_id"]).Iter().Next(&r2)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(6, len(spans))
}

func TestWrapCollection_Remove(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		collection.Remove(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.remove", spans[1].OperationName())
}

func TestWrapCollection_RemoveId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	removeByID := func(collection *WrapCollection) {
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
	assert.Equal("mongodb.removeid", spans[3].OperationName())
}

func TestWrapCollection_RemoveAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		collection.Insert(entity)
		collection.RemoveAll(entity)
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.removeall", spans[1].OperationName())
}

func TestWrapCollection_DropCollection(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		collection.DropCollection()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.dropcollection", spans[0].OperationName())
}

func TestWrapCollection_Create(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		collection.Create(&mgo.CollectionInfo{})
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.create", spans[0].OperationName())
}

func TestWrapCollection_Count(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		collection.Count()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.count", spans[0].OperationName())
}

func TestWrapCollection_IndexCommands(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		indexes, _ := collection.Indexes()
		collection.DropIndex("_id_")
		collection.DropIndexName("_id_")
		collection.DropAllIndexes()
		collection.EnsureIndex(indexes[0])
		collection.EnsureIndexKey("_id_")
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(7, len(spans))
	assert.Equal("mongodb.indexes", spans[0].OperationName())
	assert.Equal("mongodb.dropindex", spans[1].OperationName())
	assert.Equal("mongodb.dropindexname", spans[2].OperationName())
	assert.Equal("mongodb.dropallindexes", spans[3].OperationName())
	assert.Equal("mongodb.ensureindex", spans[4].OperationName())
	assert.Equal("mongodb.ensureindexkey", spans[5].OperationName())
}

func TestWrapCollection_FindAndIter(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
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
	assert.Equal("mongodb.query.iter", spans[3].OperationName())
	assert.Equal("mongodb.iter.next", spans[4].OperationName())
	assert.Equal("mongodb.iter.all", spans[5].OperationName())
	assert.Equal("mongodb.iter.close", spans[6].OperationName())
}

func TestWrapCollection_Bulk(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	insert := func(collection *WrapCollection) {
		bulk := collection.Bulk()
		bulk.Insert(entity)
		bulk.Run()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.bulk.run", spans[0].OperationName())
}
