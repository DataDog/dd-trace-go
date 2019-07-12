// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package mgo

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/globalsign/mgo"
)

// Query is an mgo.Query instance along with the data necessary for tracing.
type Query struct {
	*mgo.Query
	cfg  *mongoConfig
	tags map[string]string
}

// Iter invokes and traces Query.Iter
func (q *Query) Iter() *Iter {
	span := newChildSpanFromContext(q.cfg, q.tags)
	iter := q.Query.Iter()
	span.Finish()
	return &Iter{
		Iter: iter,
		cfg:  q.cfg,
	}
}

// All invokes and traces Query.All
func (q *Query) All(result interface{}) error {
	span := newChildSpanFromContext(q.cfg, q.tags)
	err := q.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Apply invokes and traces Query.Apply
func (q *Query) Apply(change mgo.Change, result interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(q.cfg, q.tags)
	info, err = q.Apply(change, result)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Count invokes and traces Query.Count
func (q *Query) Count() (n int, err error) {
	span := newChildSpanFromContext(q.cfg, q.tags)
	n, err = q.Count()
	span.Finish(tracer.WithError(err))
	return n, err
}

// Distinct invokes and traces Query.Distinct
func (q *Query) Distinct(key string, result interface{}) error {
	span := newChildSpanFromContext(q.cfg, q.tags)
	err := q.Distinct(key, result)
	span.Finish(tracer.WithError(err))
	return err
}

// Explain invokes and traces Query.Explain
func (q *Query) Explain(result interface{}) error {
	span := newChildSpanFromContext(q.cfg, q.tags)
	err := q.Explain(result)
	span.Finish(tracer.WithError(err))
	return err
}

// For invokes and traces Query.For
func (q *Query) For(result interface{}, f func() error) error {
	span := newChildSpanFromContext(q.cfg, q.tags)
	err := q.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// MapReduce invokes and traces Query.MapReduce
func (q *Query) MapReduce(job *mgo.MapReduce, result interface{}) (info *mgo.MapReduceInfo, err error) {
	span := newChildSpanFromContext(q.cfg, q.tags)
	info, err = q.MapReduce(job, result)
	span.Finish(tracer.WithError(err))
	return info, err
}

// One invokes and traces Query.One
func (q *Query) One(result interface{}) error {
	span := newChildSpanFromContext(q.cfg, q.tags)
	err := q.One(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Tail invokes and traces Query.Tail
func (q *Query) Tail(timeout time.Duration) *Iter {
	span := newChildSpanFromContext(q.cfg, q.tags)
	iter := q.Query.Tail(timeout)
	span.Finish()
	return &Iter{
		Iter: iter,
		cfg:  q.cfg,
	}
}
