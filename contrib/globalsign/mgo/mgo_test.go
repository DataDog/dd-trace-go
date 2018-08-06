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

	session, err := Dial(ctx, "192.168.33.10:27017")
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
}

func TestWrapCollection_DropCollection(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		collection.DropCollection()
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
}

func TestWrapCollection_Create(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *WrapCollection) {
		collection.Create(&mgo.CollectionInfo{})
	}

	spans := testMongoCollectionCommand(assert, insert)
	assert.Equal(2, len(spans))
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
