package mgo

import (
	"context"
	"fmt"

	"github.com/globalsign/mgo"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type mongoConfig struct {
	serviceName string
}

// MongoOption represents an option that can be passed to Dial
type MongoOption func(*mongoConfig)

func defaults(cfg *mongoConfig) {
	cfg.serviceName = "mongodb"
}

// WithServiceName sets the service name for a given MongoDB context.
func WithServiceName(name string) MongoOption {
	return func(cfg *mongoConfig) {
		cfg.serviceName = name
	}
}

// Dial opens a connection to a MongoDB server and ties it to the
// given context for tracing MongoDB calls.
func Dial(ctx context.Context, url string, opts ...MongoOption) (*Session, error) {
	session, err := mgo.Dial(url)
	s := &Session{
		Session: session,
		ctx:     ctx,
	}

	defaults(&s.cfg)
	for _, fn := range opts {
		fn(&s.cfg)
	}

	return s, err
}

type Session struct {
	*mgo.Session

	ctx context.Context
	cfg mongoConfig
}

func newChildSpanFromContext(ctx context.Context, config mongoConfig, resource string, op string) ddtrace.Span {
	name := fmt.Sprintf("%s", op)
	span, _ := tracer.StartSpanFromContext(
		ctx,
		name,
		tracer.SpanType("mongodb"),
		tracer.ServiceName(config.serviceName))
	span.SetTag("resource.name", resource)

	return span
}

func (s *Session) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.ctx, s.cfg, "mongodb.query", "mongodb.query")
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

func (s *Session) WithContext(ctx context.Context) *Session {
	return &Session{
		Session: s.Session,
		ctx:     ctx,
		cfg:     s.cfg,
	}
}

type Database struct {
	*mgo.Database
	ctx context.Context
	cfg mongoConfig
}

func (s *Session) DB(name string) *Database {
	return &Database{
		Database: s.Session.DB(name),
		ctx:      s.ctx,
		cfg:      s.cfg}
}

func (db *Database) WithContext(ctx context.Context) *Database {
	return &Database{
		Database: db.Database,
		ctx:      ctx,
		cfg:      db.cfg}
}

type Collection struct {
	*mgo.Collection
	ctx context.Context
	cfg mongoConfig
}

func (db *Database) C(name string) *Collection {
	return &Collection{
		Collection: db.Database.C(name),
		ctx:        db.ctx,
		cfg:        db.cfg}
}

func (c *Collection) WithContext(ctx context.Context) *Collection {
	return &Collection{
		Collection: c.Collection,
		ctx:        ctx,
		cfg:        c.cfg}
}

func (c *Collection) Insert(docs ...interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.insert")
	err := c.Collection.Insert(docs...)
	span.Finish(tracer.WithError(err))
	return err
}

func (c *Collection) Find(query interface{}) *Query {
	return &Query{
		Query: c.Collection.Find(query),
		ctx:   c.ctx,
		cfg:   c.cfg}
}

type Query struct {
	*mgo.Query
	ctx context.Context
	cfg mongoConfig
}

func (q *Query) Iter() *Iter {
	span := newChildSpanFromContext(q.ctx, q.cfg, "mongodb.query", "mongodb.query.iter")
	iter := q.Query.Iter()
	span.Finish()
	return &Iter{
		Iter: iter,
		ctx:  q.ctx,
		cfg:  q.cfg,
	}
}

type Iter struct {
	*mgo.Iter

	ctx context.Context
	cfg mongoConfig
}

func (i *Iter) Next(result interface{}) bool {
	span := newChildSpanFromContext(i.ctx, i.cfg, "mongodb.query", "mongodb.iter.next")
	r := i.Iter.Next(result)
	span.Finish()
	return r
}
