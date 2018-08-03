package mgo

import (
	"context"
	"fmt"

	"github.com/globalsign/mgo"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Session struct {
	*mgo.Session

	ctx         context.Context
	serviceName string
}

func newChildSpanFromContext(ctx context.Context, serviceName string, resource string, op string) ddtrace.Span {
	name := fmt.Sprintf("%s", op)
	span, _ := tracer.StartSpanFromContext(
		ctx,
		name,
		tracer.SpanType("mongodb"),
		tracer.ServiceName(serviceName))
	span.SetTag("resource.name", resource)

	return span
}

func (s *Session) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.ctx, s.serviceName, "mongodb.query", "mongodb.query")
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

func (s *Session) WithContext(ctx context.Context) *Session {
	return &Session{
		Session: s.Session,
		ctx:     ctx,
	}
}

type Database struct {
	*mgo.Database
	ctx context.Context
}

func (s *Session) DB(name string) *Database {
	return &Database{
		Database: s.Session.DB(name),
		ctx:      s.ctx}
}

func (db *Database) WithContext(ctx context.Context) *Database {
	return &Database{
		Database: db.Database,
		ctx:      ctx,
	}
}

type Collection struct {
	*mgo.Collection
	ctx context.Context
}

func (db *Database) C(name string) *Collection {
	return &Collection{
		Collection: db.Database.C(name),
		ctx:        db.ctx,
	}
}

func (c *Collection) WithContext(ctx context.Context) *Collection {
	return &Collection{
		Collection: c.Collection,
		ctx:        ctx,
	}
}

func (c *Collection) Insert(docs ...interface{}) error {
	span := newChildSpanFromContext(c.ctx, "ERICH", "mongodb.query", "mongodb.query")
	err := c.Collection.Insert(docs...)
	span.Finish(tracer.WithError(err))
	return err
}
