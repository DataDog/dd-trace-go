// Package mgo provides functions and types which allow tracing of the MGO MongoDB client (https://github.com/globalsign/mgo)
package mgo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/globalsign/mgo"

import (
	"context"

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

// Session wraps a Session with the Context and Config information
// needed to support tracing.
type Session struct {
	*mgo.Session

	ctx context.Context
	cfg mongoConfig
}

func newChildSpanFromContext(ctx context.Context, config mongoConfig) ddtrace.Span {
	name := "mongodb.query"
	span, _ := tracer.StartSpanFromContext(
		ctx,
		name,
		tracer.SpanType("mongodb"),
		tracer.ServiceName(config.serviceName),
		tracer.ResourceName("mongodb.query"))

	return span
}

// Run invokes and traces Session.Run
func (s *Session) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.ctx, s.cfg)
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

// WithContext makes a copy of the `WrapSession` and sets the context
// to the context given in the `ctx` parameter.
func (s *Session) WithContext(ctx context.Context) *Session {
	return &Session{
		Session: s.Session,
		ctx:     ctx,
		cfg:     s.cfg,
	}
}

// Database wraps a Database with the Context and Config information
// needed to support tracing.
type Database struct {
	*mgo.Database
	ctx context.Context
	cfg mongoConfig
}

// DB invokes Session.DB and wraps it with the context and config
// data from `WrapSession` that is needed to support tracing
func (s *Session) DB(name string) *Database {
	return &Database{
		Database: s.Session.DB(name),
		ctx:      s.ctx,
		cfg:      s.cfg}
}

// WithContext makes a copy of the `WrapDatabase` and sets the context
// to the context given in the `ctx` parameter.
func (db *Database) WithContext(ctx context.Context) *Database {
	return &Database{
		Database: db.Database,
		ctx:      ctx,
		cfg:      db.cfg}
}

// Collection wraps an mgo.Collection type with the context
// and config needed to support tracing.
type Collection struct {
	*mgo.Collection
	ctx context.Context
	cfg mongoConfig
}

// C gets a Collection from the Mongo DB and wraps it with
// the context and configuration from the WrapDatabase.
func (db *Database) C(name string) *Collection {
	return &Collection{
		Collection: db.Database.C(name),
		ctx:        db.ctx,
		cfg:        db.cfg}
}

// WithContext creates a copy of the WrapCollection but with the context set
// to context given in the `ctx` parameter.
func (c *Collection) WithContext(ctx context.Context) *Collection {
	return &Collection{
		Collection: c.Collection,
		ctx:        ctx,
		cfg:        c.cfg}
}

// Create invokes and traces Collection.Create
func (c *Collection) Create(info *mgo.CollectionInfo) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.Create(info)
	span.Finish(tracer.WithError(err))
	return err
}

// DropCollection invokes and traces Collection.DropCollection
func (c *Collection) DropCollection() error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.DropCollection()
	span.Finish(tracer.WithError(err))
	return err
}

// EnsureIndexKey invokes and traces Collection.EnsureIndexKey
func (c *Collection) EnsureIndexKey(key ...string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.EnsureIndexKey(key...)
	span.Finish(tracer.WithError(err))
	return err
}

// EnsureIndex invokes and traces Collection.EnsureIndex
func (c *Collection) EnsureIndex(index mgo.Index) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.EnsureIndex(index)
	span.Finish(tracer.WithError(err))
	return err
}

// DropIndex invokes and traces Collection.DropIndex
func (c *Collection) DropIndex(key ...string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.DropIndex(key...)
	span.Finish(tracer.WithError(err))
	return err
}

// DropIndexName invokes and traces Collection.DropIndexName
func (c *Collection) DropIndexName(name string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.DropIndexName(name)
	span.Finish(tracer.WithError(err))
	return err
}

// DropAllIndexes invokes and traces Collection.DropAllIndexes
func (c *Collection) DropAllIndexes() error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.DropAllIndexes()
	span.Finish(tracer.WithError(err))
	return err
}

// Indexes invokes and traces Collection.Indexes
func (c *Collection) Indexes() (indexes []mgo.Index, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	indexes, err = c.Collection.Indexes()
	span.Finish(tracer.WithError(err))
	return indexes, err
}

// Insert invokes and traces Collectin.Insert
func (c *Collection) Insert(docs ...interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.Insert(docs...)
	span.Finish(tracer.WithError(err))
	return err
}

// Find invokes and traces Collection.Find
func (c *Collection) Find(query interface{}) *Query {
	return &Query{
		Query: c.Collection.Find(query),
		ctx:   c.ctx,
		cfg:   c.cfg}
}

// FindId invokes and traces Collection.FindId
func (c *Collection) FindId(id interface{}) *Query { // nolint
	return &Query{
		Query: c.Collection.FindId(id),
		ctx:   c.ctx,
		cfg:   c.cfg}
}

// Count invokes and traces Collection.Count
func (c *Collection) Count() (n int, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	n, err = c.Collection.Count()
	span.Finish(tracer.WithError(err))
	return n, err
}

// Bulk creates a trace ready wrapper around Collection.Bulk
func (c *Collection) Bulk() *Bulk {
	return &Bulk{
		Bulk: c.Collection.Bulk(),
		ctx:  c.ctx,
		cfg:  c.cfg,
	}
}

// Update invokes and traces Collection.Update
func (c *Collection) Update(selector interface{}, update interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.Update(selector, update)
	span.Finish(tracer.WithError(err))
	return err
}

// UpdateId invokes and traces Collection.UpdateId
func (c *Collection) UpdateId(id interface{}, update interface{}) error { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.UpdateId(id, update)
	span.Finish(tracer.WithError(err))
	return err
}

// UpdateAll invokes and traces Collection.UpdateAll
func (c *Collection) UpdateAll(selector interface{}, update interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	info, err = c.Collection.UpdateAll(selector, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Upsert invokes and traces Collection.Upsert
func (c *Collection) Upsert(selector interface{}, update interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	info, err = c.Collection.Upsert(selector, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// UpsertId invokes and traces Collection.UpsertId
func (c *Collection) UpsertId(id interface{}, update interface{}) (info *mgo.ChangeInfo, err error) { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg)
	info, err = c.Collection.UpsertId(id, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Remove invokes and traces Collection.Remove
func (c *Collection) Remove(selector interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.Remove(selector)
	span.Finish(tracer.WithError(err))
	return err
}

// RemoveId invokes and traces Collection.RemoveId
func (c *Collection) RemoveId(id interface{}) error { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg)
	err := c.Collection.RemoveId(id)
	span.Finish(tracer.WithError(err))
	return err
}

// RemoveAll invokes and traces Collection.RemoveAll
func (c *Collection) RemoveAll(selector interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	info, err = c.Collection.RemoveAll(selector)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Repair invokes and traces Collection.Repair
func (c *Collection) Repair() *Iter {
	span := newChildSpanFromContext(c.ctx, c.cfg)
	iter := c.Collection.Repair()
	span.Finish()
	return &Iter{
		Iter: iter,
		ctx:  c.ctx,
		cfg:  c.cfg,
	}
}

// Query wraps the Query type with the context and config
// needed to support tracing.
type Query struct {
	*mgo.Query
	ctx context.Context
	cfg mongoConfig
}

// Iter invokes and traces Query.Iter
func (q *Query) Iter() *Iter {
	span := newChildSpanFromContext(q.ctx, q.cfg)
	iter := q.Query.Iter()
	span.Finish()
	return &Iter{
		Iter: iter,
		ctx:  q.ctx,
		cfg:  q.cfg,
	}
}

// Iter wraps a Session with the Context and Config information
// needed to support tracing.
type Iter struct {
	*mgo.Iter

	ctx context.Context
	cfg mongoConfig
}

// Next invokes and traces Iter.Next
func (iter *Iter) Next(result interface{}) bool {
	span := newChildSpanFromContext(iter.ctx, iter.cfg)
	r := iter.Iter.Next(result)
	span.Finish()
	return r
}

// For invokes and traces Iter.For
func (iter *Iter) For(result interface{}, f func() error) (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg)
	err = iter.Iter.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// All invokes and traces Iter.All
func (iter *Iter) All(result interface{}) (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg)
	err = iter.Iter.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Close invokes and traces Iter.Close
func (iter *Iter) Close() (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg)
	err = iter.Iter.Close()
	span.Finish(tracer.WithError(err))
	return err
}

// Bulk wraps a Session with the Context and Config information
// needed to support tracing.
type Bulk struct {
	*mgo.Bulk

	ctx context.Context
	cfg mongoConfig
}

// Run invokes and traces Bulk.Run
func (b *Bulk) Run() (result *mgo.BulkResult, err error) {
	span := newChildSpanFromContext(b.ctx, b.cfg)
	result, err = b.Bulk.Run()
	span.Finish(tracer.WithError(err))

	return result, err
}
