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

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testMongoCollectionCommand(t *testing.T, command func(*Collection)) []mocktracer.Span {
	assert := assert.New(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	parentSpan, ctx := tracer.StartSpanFromContext(
		context.Background(),
		"mgo-unittest",
		tracer.SpanType("app"),
		tracer.ResourceName("insert-test"),
	)

	session, err := Dial("localhost:27017", WithServiceName("unit-tests"), WithContext(ctx))
	require.NoError(t, err)

	defer session.Close()

	db := session.DB("my_db")
	collection := db.C("MyCollection")

	command(collection)

	parentSpan.Finish()

	spans := mt.FinishedSpans()

	for _, val := range spans {
		if val.OperationName() == "mongodb.query" {
			assert.Equal("globalsign/mgo", val.Tag(ext.Component))
			assert.Equal("MyCollection", val.Tag(ext.MongoDBCollection))
			assert.Equal("localhost", val.Tag(ext.NetworkDestinationName))
		}
	}

	return spans
}

func TestIter_NoSpanKind(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpdateId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(5, len(spans))

	numSpanKindClient := 0
	for _, val := range spans {
		if val.OperationName() != "mgo-unittest" {
			assert.Equal("mongodb", val.Tag(ext.DBSystem))
			if val, ok := val.Tags()[ext.SpanKind]; ok && val == ext.SpanKindClient {
				numSpanKindClient++
			}
		}
	}
	assert.Equal(3, numSpanKindClient, "Iter() should not get span.kind tag")
}

func TestCollection_Insert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
	assert.Equal(ext.SpanKindClient, spans[0].Tag(ext.SpanKind))
	assert.Equal("mongodb", spans[0].Tag(ext.DBSystem))
	assert.Equal("localhost", spans[0].Tag(ext.NetworkDestinationName))
}

func TestCollection_Update(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Update(entity, entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal(ext.SpanKindClient, spans[1].Tag(ext.SpanKind))
	assert.Equal("mongodb", spans[1].Tag(ext.DBSystem))
	assert.Equal("localhost", spans[0].Tag(ext.NetworkDestinationName))
}

func TestCollection_UpdateId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpdateId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(5, len(spans))
	assert.Equal("mongodb.query", spans[3].OperationName())
	assert.Equal("mongodb", spans[3].Tag(ext.DBSystem))
}

func TestIssue874(t *testing.T) {
	// regression test for DataDog/dd-trace-go#873
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

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

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(9, len(spans))
}

func TestCollection_Upsert(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Upsert(entity, entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		collection.UpsertId(r.Map()["_id"], entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(6, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb", spans[1].Tag(ext.DBSystem))
	assert.Equal("mongodb.query", spans[4].OperationName())
	assert.Equal("mongodb", spans[4].Tag(ext.DBSystem))
}

func TestCollection_UpdateAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.UpdateAll(entity, entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb", spans[1].Tag(ext.DBSystem))
}

func TestCollection_FindId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		var r bson.D
		collection.Find(entity).Iter().Next(&r)
		var r2 bson.D
		collection.FindId(r.Map()["_id"]).Iter().Next(&r2)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(6, len(spans))
}

func TestCollection_Remove(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.Remove(entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb", spans[1].Tag(ext.DBSystem))
}

func TestCollection_RemoveId(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

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

	spans := testMongoCollectionCommand(t, removeByID)
	assert.Equal(5, len(spans))
	assert.Equal("mongodb.query", spans[3].OperationName())
	assert.Equal("mongodb", spans[3].Tag(ext.DBSystem))
}

func TestCollection_RemoveAll(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		collection.Insert(entity)
		collection.RemoveAll(entity)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(3, len(spans))
	assert.Equal("mongodb.query", spans[1].OperationName())
	assert.Equal("mongodb", spans[1].Tag(ext.DBSystem))
}

func TestCollection_DropCollection(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.DropCollection()
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
	assert.Equal("mongodb", spans[0].Tag(ext.DBSystem))
}

func TestCollection_Create(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.Create(&mgo.CollectionInfo{})
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
	assert.Equal("mongodb", spans[0].Tag(ext.DBSystem))
}

func TestCollection_Count(t *testing.T) {
	assert := assert.New(t)

	insert := func(collection *Collection) {
		collection.Count()
	}

	spans := testMongoCollectionCommand(t, insert)
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

	spans := testMongoCollectionCommand(t, indexTest)
	require.Equal(t, 6, len(spans))
	for i := 0; i <= 4; i++ {
		span := spans[i]
		assert.Equal("mongodb.query", span.OperationName())
		assert.Equal("mongodb", span.Tag(ext.DBSystem))
	}
	assert.Equal("mgo-unittest", spans[5].OperationName())
}

func TestCollection_FindAndIter(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

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

	spans := testMongoCollectionCommand(t, insert)
	require.Equal(t, 8, len(spans))
	for i := 3; i <= 6; i++ {
		span := spans[i]
		assert.Equal("mongodb.query", span.OperationName())
		assert.Equal("mongodb", span.Tag(ext.DBSystem))
	}
}

func TestCollection_Bulk(t *testing.T) {
	assert := assert.New(t)

	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0,
			},
		},
	}

	insert := func(collection *Collection) {
		bulk := collection.Bulk()
		bulk.Insert(entity)
		bulk.Run()
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(2, len(spans))
	assert.Equal("mongodb.query", spans[0].OperationName())
	assert.Equal("mongodb", spans[0].Tag(ext.DBSystem))
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

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []DialOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		session, err := Dial("localhost:27017", opts...)
		require.NoError(t, err)
		err = session.
			DB("my_db").
			C("MyCollection").
			Insert(bson.D{bson.DocElem{Name: "entity", Value: bson.DocElem{Name: "index", Value: 0}}})
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	namingschematest.NewMongoDBTest(genSpans, "mongodb")(t)
}

func TestIssue2165(t *testing.T) {
	assert := assert.New(t)
	insert := func(collection *Collection) {
		p := collection.Pipe(bson.M{})
		p.One(nil)
		p.Explain(nil)
	}

	spans := testMongoCollectionCommand(t, insert)
	assert.Equal(3, len(spans))

	for _, val := range spans {
		if val.OperationName() != "mgo-unittest" {
			assert.Equal("mongodb", val.Tag(ext.DBSystem))
			if err, ok := val.Tags()[ext.Error]; ok {
				assert.NotNil(err)
			}
		}
	}
}
