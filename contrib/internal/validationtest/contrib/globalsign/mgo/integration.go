// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	"testing"

	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/require"

	mgotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/globalsign/mgo"
)

type Integration struct {
	collection *mgotrace.Collection
	numSpans   int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/globalsign/mgo"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	// connect to MongoDB
	session, err := mgotrace.Dial("localhost:27017")
	require.NoError(t, err)

	db := session.DB("my_db")
	i.collection = db.C("MyCollection")

	return func() {
		session.Close()
	}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	TestCollection_Insert(t, i)
	i.numSpans++

	TestCollection_Update(t, i)
	i.numSpans++

	TestCollection_Remove(t, i)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func TestCollection_Insert(t *testing.T, i *Integration) {
	entity := bson.D{
		bson.DocElem{
			Name: "entity",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}
	i.collection.Insert(entity)
	i.numSpans++
}

func TestCollection_Update(t *testing.T, i *Integration) {
	entity := bson.D{
		bson.DocElem{
			Name: "entity_new",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	i.collection.Update(entity, entity)
}

func TestCollection_Remove(t *testing.T, i *Integration) {
	entity := bson.D{
		bson.DocElem{
			Name: "entity_new",
			Value: bson.DocElem{
				Name:  "index",
				Value: 0}}}

	i.collection.Remove(entity)
}
