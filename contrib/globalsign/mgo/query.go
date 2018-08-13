package mgo

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/globalsign/mgo"
)

// Query is an mgo.Query instance along with the data necessary for tracing.
type Query struct {
	*mgo.Query
	query string
	cfg   mongoConfig
}

// Iter invokes and traces Query.Iter
func (q *Query) Iter() *Iter {
	span := newChildSpanFromContext(q.cfg)
	iter := q.Query.Iter()
	span.Finish()
	return &Iter{
		Iter: iter,
		cfg:  q.cfg,
	}
}

// All invokes and traces Query.All
func (q *Query) All(result interface{}) error {
	span := newChildSpanFromContext(q.cfg)
	err := q.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Apply invokes and traces Query.Apply
func (q *Query) Apply(change mgo.Change, result interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(q.cfg)
	info, err = q.Apply(change, result)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Batch invokes and traces Query.Batch
func (q *Query) Batch(n int) *Query {
	return &Query{
		Query: q.Query.Batch(n),
		cfg:   q.cfg,
	}
}

// Collation invokes and traces Query.Collation
func (q *Query) Collation(collation *mgo.Collation) *Query {
	return &Query{
		Query: q.Query.Collation(collation),
		cfg:   q.cfg,
	}
}

// Comment invokes and traces Query.Comment
func (q *Query) Comment(comment string) *Query {
	return &Query{
		Query: q.Query.Comment(comment),
		cfg:   q.cfg,
	}
}

// Count invokes and traces Query.Count
func (q *Query) Count() (n int, err error) {
	span := newChildSpanFromContext(q.cfg)
	n, err = q.Count()
	span.Finish(tracer.WithError(err))
	return n, err
}

// Distinct invokes and traces Query.Distinct
func (q *Query) Distinct(key string, result interface{}) error {
	span := newChildSpanFromContext(q.cfg)
	err := q.Distinct(key, result)
	span.Finish(tracer.WithError(err))
	return err
}

// Explain invokes and traces Query.Explain
func (q *Query) Explain(result interface{}) error {
	span := newChildSpanFromContext(q.cfg)
	err := q.Explain(result)
	span.Finish(tracer.WithError(err))
	return err
}

// For invokes and traces Query.For
func (q *Query) For(result interface{}, f func() error) error {
	span := newChildSpanFromContext(q.cfg)
	err := q.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// MapReduce invokes and traces Query.MapReduce
func (q *Query) MapReduce(job *mgo.MapReduce, result interface{}) (info *mgo.MapReduceInfo, err error) {
	span := newChildSpanFromContext(q.cfg)
	info, err = q.MapReduce(job, result)
	span.Finish(tracer.WithError(err))
	return info, err
}

// One invokes and traces Query.One
func (q *Query) One(result interface{}) error {
	span := newChildSpanFromContext(q.cfg)
	err := q.One(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Prefetch invokes Query.Prefetch and configures the
// returned *Query for tracing.
func (q *Query) Prefetch(p float64) *Query {
	return &Query{
		Query: q.Query.Prefetch(p),
		cfg:   q.cfg,
	}
}

// Select invokes Query.Select and configures the
// returned *Query for tracing.
func (q *Query) Select(selector interface{}) *Query {
	return &Query{
		Query: q.Query.Select(selector),
		cfg:   q.cfg,
	}
}

// SetMaxScan invokes and traces Query.SetMaxScan
func (q *Query) SetMaxScan(n int) *Query {
	return &Query{
		Query: q.Query.SetMaxScan(n),
		cfg:   q.cfg,
	}
}

// SetMaxTime invokes and traces Query.SetMaxTime
func (q *Query) SetMaxTime(d time.Duration) *Query {
	return &Query{
		Query: q.Query.SetMaxTime(d),
		cfg:   q.cfg,
	}
}

// Skip invokes and traces Query.Skip
func (q *Query) Skip(n int) *Query {
	return &Query{
		Query: q.Query.Skip(n),
		cfg:   q.cfg,
	}
}

// Snapshot invokes and traces Query.Snapshot
func (q *Query) Snapshot() *Query {
	return &Query{
		Query: q.Query.Snapshot(),
		cfg:   q.cfg,
	}
}

// Sort invokes and traces Query.Sort
func (q *Query) Sort(fields ...string) *Query {
	return &Query{
		Query: q.Query.Sort(fields...),
		cfg:   q.cfg,
	}
}

// Tail invokes and traces Query.Tail
func (q *Query) Tail(timeout time.Duration) *Iter {
	span := newChildSpanFromContext(q.cfg)
	iter := q.Query.Tail(timeout)
	span.Finish()
	return &Iter{
		Iter: iter,
		cfg:  q.cfg,
	}
}
