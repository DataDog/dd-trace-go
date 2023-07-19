// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

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
	opts       []mgotrace.DialOption
}

func New() *Integration {
	return &Integration{
		opts: make([]mgotrace.DialOption, 0),
	}
}

func (i *Integration) Name() string {
	return "globalsign/mgo"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	// connect to MongoDB
	session, err := mgotrace.Dial("localhost:27017", i.opts...)
	require.NoError(t, err)

	db := session.DB("my_db")
	i.collection = db.C("MyCollection")

	t.Cleanup(func() {
		session.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.collection.Insert(entity)
	i.numSpans++

	i.collection.Update(entity, entity)
	i.numSpans++

	i.collection.Remove(entity)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, mgotrace.WithServiceName(name))
}

var entity = bson.D{
	bson.DocElem{
		Name: "entity_new",
		Value: bson.DocElem{
			Name:  "index",
			Value: 0}}}
