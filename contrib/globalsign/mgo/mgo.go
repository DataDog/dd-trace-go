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
func Dial(ctx context.Context, url string, opts ...MongoOption) (*WrapSession, error) {
	session, err := mgo.Dial(url)
	s := &WrapSession{
		Session: session,
		ctx:     ctx,
	}

	defaults(&s.cfg)
	for _, fn := range opts {
		fn(&s.cfg)
	}

	return s, err
}

// WrapSession wraps a Session with the Context and Config information
// needed to support tracing.
type WrapSession struct {
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

// Run invokes and traces Session.Run
func (s *WrapSession) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.ctx, s.cfg, "mongodb.query", "mongodb.query")
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

// WithContext makes a copy of the `WrapSession` and sets the context
// to the context given in the `ctx` parameter.
func (s *WrapSession) WithContext(ctx context.Context) *WrapSession {
	return &WrapSession{
		Session: s.Session,
		ctx:     ctx,
		cfg:     s.cfg,
	}
}

// WrapDatabase wraps a Database with the Context and Config information
// needed to support tracing.
type WrapDatabase struct {
	*mgo.Database
	ctx context.Context
	cfg mongoConfig
}

// DB invokes Session.DB and wraps it with the context and config
// data from `WrapSession` that is needed to support tracing
func (s *WrapSession) DB(name string) *WrapDatabase {
	return &WrapDatabase{
		Database: s.Session.DB(name),
		ctx:      s.ctx,
		cfg:      s.cfg}
}

// WithContext makes a copy of the `WrapDatabase` and sets the context
// to the context given in the `ctx` parameter.
func (db *WrapDatabase) WithContext(ctx context.Context) *WrapDatabase {
	return &WrapDatabase{
		Database: db.Database,
		ctx:      ctx,
		cfg:      db.cfg}
}

// WrapCollection wraps an mgo.Collection type with the context
// and config needed to support tracing.
type WrapCollection struct {
	*mgo.Collection
	ctx context.Context
	cfg mongoConfig
}

// C gets a Collection from the Mongo DB and wraps it with
// the context and configuration from the WrapDatabase.
func (db *WrapDatabase) C(name string) *WrapCollection {
	return &WrapCollection{
		Collection: db.Database.C(name),
		ctx:        db.ctx,
		cfg:        db.cfg}
}

// WithContext creates a copy of the WrapCollection but with the context set
// to context given in the `ctx` parameter.
func (c *WrapCollection) WithContext(ctx context.Context) *WrapCollection {
	return &WrapCollection{
		Collection: c.Collection,
		ctx:        ctx,
		cfg:        c.cfg}
}

// Create invokes and traces Collection.Create
func (c *WrapCollection) Create(info *mgo.CollectionInfo) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.create")
	err := c.Collection.Create(info)
	span.Finish(tracer.WithError(err))
	return err
}

// DropCollection invokes and traces Collection.DropCollection
func (c *WrapCollection) DropCollection() error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.dropcollection")
	err := c.Collection.DropCollection()
	span.Finish(tracer.WithError(err))
	return err
}

// EnsureIndexKey invokes and traces Collection.EnsureIndexKey
func (c *WrapCollection) EnsureIndexKey(key ...string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.ensureindexkey")
	err := c.Collection.EnsureIndexKey(key...)
	span.Finish(tracer.WithError(err))
	return err
}

// EnsureIndex invokes and traces Collection.EnsureIndex
func (c *WrapCollection) EnsureIndex(index mgo.Index) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.ensureindex")
	err := c.Collection.EnsureIndex(index)
	span.Finish(tracer.WithError(err))
	return err
}

// DropIndex invokes and traces Collection.DropIndex
func (c *WrapCollection) DropIndex(key ...string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.dropindex")
	err := c.Collection.DropIndex(key...)
	span.Finish(tracer.WithError(err))
	return err
}

// DropIndexName invokes and traces Collection.DropIndexName
func (c *WrapCollection) DropIndexName(name string) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.dropindexname")
	err := c.Collection.DropIndexName(name)
	span.Finish(tracer.WithError(err))
	return err
}

// DropAllIndexes invokes and traces Collection.DropAllIndexes
func (c *WrapCollection) DropAllIndexes() error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.dropallindexes")
	err := c.Collection.DropAllIndexes()
	span.Finish(tracer.WithError(err))
	return err
}

// Indexes invokes and traces Collection.Indexes
func (c *WrapCollection) Indexes() (indexes []mgo.Index, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.indexes")
	indexes, err = c.Collection.Indexes()
	span.Finish(tracer.WithError(err))
	return indexes, err
}

// Insert invokes and traces Collectin.Insert
func (c *WrapCollection) Insert(docs ...interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.insert")
	err := c.Collection.Insert(docs...)
	span.Finish(tracer.WithError(err))
	return err
}

// Find invokes and traces Collection.Find
func (c *WrapCollection) Find(query interface{}) *WrapQuery {
	return &WrapQuery{
		Query: c.Collection.Find(query),
		ctx:   c.ctx,
		cfg:   c.cfg}
}

// FindId invokes and traces Collection.FindId
func (c *WrapCollection) FindId(id interface{}) *WrapQuery { // nolint
	return &WrapQuery{
		Query: c.Collection.FindId(id),
		ctx:   c.ctx,
		cfg:   c.cfg}
}

// Count invokes and traces Collection.Count
func (c *WrapCollection) Count() (n int, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.count")
	n, err = c.Collection.Count()
	span.Finish(tracer.WithError(err))
	return n, err
}

// Bulk creates a trace ready wrapper around Collection.Bulk
func (c *WrapCollection) Bulk() *WrapBulk {
	return &WrapBulk{
		Bulk: c.Collection.Bulk(),
		ctx:  c.ctx,
		cfg:  c.cfg,
	}
}

// Update invokes and traces Collection.Update
func (c *WrapCollection) Update(selector interface{}, update interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.update")
	err := c.Collection.Update(selector, update)
	span.Finish(tracer.WithError(err))
	return err
}

// UpdateId invokes and traces Collection.UpdateId
func (c *WrapCollection) UpdateId(id interface{}, update interface{}) error { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.updateid")
	err := c.Collection.UpdateId(id, update)
	span.Finish(tracer.WithError(err))
	return err
}

// UpdateAll invokes and traces Collection.UpdateAll
func (c *WrapCollection) UpdateAll(selector interface{}, update interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.updateall")
	info, err = c.Collection.UpdateAll(selector, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Upsert invokes and traces Collection.Upsert
func (c *WrapCollection) Upsert(selector interface{}, update interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.upsert")
	info, err = c.Collection.Upsert(selector, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// UpsertId invokes and traces Collection.UpsertId
func (c *WrapCollection) UpsertId(id interface{}, update interface{}) (info *mgo.ChangeInfo, err error) { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.upsertid")
	info, err = c.Collection.UpsertId(id, update)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Remove invokes and traces Collection.Remove
func (c *WrapCollection) Remove(selector interface{}) error {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.remove")
	err := c.Collection.Remove(selector)
	span.Finish(tracer.WithError(err))
	return err
}

// RemoveId invokes and traces Collection.RemoveId
func (c *WrapCollection) RemoveId(id interface{}) error { // nolint
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.removeid")
	err := c.Collection.RemoveId(id)
	span.Finish(tracer.WithError(err))
	return err
}

// RemoveAll invokes and traces Collection.RemoveAll
func (c *WrapCollection) RemoveAll(selector interface{}) (info *mgo.ChangeInfo, err error) {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.removeall")
	info, err = c.Collection.RemoveAll(selector)
	span.Finish(tracer.WithError(err))
	return info, err
}

// Repair invokes and traces Collection.Repair
func (c *WrapCollection) Repair() *WrapIter {
	span := newChildSpanFromContext(c.ctx, c.cfg, "mongodb.query", "mongodb.repair")
	iter := c.Collection.Repair()
	span.Finish()
	return &WrapIter{
		Iter: iter,
		ctx:  c.ctx,
		cfg:  c.cfg,
	}
}

// WrapQuery wraps the Query type with the context and config
// needed to support tracing.
type WrapQuery struct {
	*mgo.Query
	ctx context.Context
	cfg mongoConfig
}

// Iter invokes and traces Query.Iter
func (q *WrapQuery) Iter() *WrapIter {
	span := newChildSpanFromContext(q.ctx, q.cfg, "mongodb.query", "mongodb.query.iter")
	iter := q.Query.Iter()
	span.Finish()
	return &WrapIter{
		Iter: iter,
		ctx:  q.ctx,
		cfg:  q.cfg,
	}
}

// WrapIter wraps a Session with the Context and Config information
// needed to support tracing.
type WrapIter struct {
	*mgo.Iter

	ctx context.Context
	cfg mongoConfig
}

// Next invokes and traces Iter.Next
func (iter *WrapIter) Next(result interface{}) bool {
	span := newChildSpanFromContext(iter.ctx, iter.cfg, "mongodb.query", "mongodb.iter.next")
	r := iter.Iter.Next(result)
	span.Finish()
	return r
}

// For invokes and traces Iter.For
func (iter *WrapIter) For(result interface{}, f func() error) (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg, "mongodb.query", "mongodb.iter.for")
	err = iter.Iter.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// All invokes and traces Iter.All
func (iter *WrapIter) All(result interface{}) (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg, "mongodb.query", "mongodb.iter.all")
	err = iter.Iter.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Close invokes and traces Iter.Close
func (iter *WrapIter) Close() (err error) {
	span := newChildSpanFromContext(iter.ctx, iter.cfg, "mongodb.query", "mongodb.iter.close")
	err = iter.Iter.Close()
	span.Finish(tracer.WithError(err))
	return err
}

// WrapBulk wraps a Session with the Context and Config information
// needed to support tracing.
type WrapBulk struct {
	*mgo.Bulk

	ctx context.Context
	cfg mongoConfig
}

// Run invokes and traces Bulk.Run
func (b *WrapBulk) Run() (result *mgo.BulkResult, err error) {
	span := newChildSpanFromContext(b.ctx, b.cfg, "mongodb.query", "mongodb.bulk.run")
	result, err = b.Bulk.Run()
	span.Finish(tracer.WithError(err))

	return result, err
}
