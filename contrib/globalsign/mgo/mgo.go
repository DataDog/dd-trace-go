// Package mgo provides functions and types which allow tracing of the MGO MongoDB client (https://github.com/globalsign/mgo)
package mgo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/globalsign/mgo"

import (
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/globalsign/mgo"
)

// Dial opens a connection to a MongoDB server and configures it
// for tracing.
func Dial(url string, opts ...DialOption) (*Session, error) {
	session, err := mgo.Dial(url)
	s := &Session{Session: session}

	defaults(&s.cfg)
	for _, fn := range opts {
		fn(&s.cfg)
	}

	// Record metadata so that it can be added to recorded traces
	s.cfg.tags["hosts"] = strings.Join(session.LiveServers(), ", ")
	info, _ := session.BuildInfo()
	s.cfg.tags["mgo_version"] = info.Version

	return s, err
}

// Session is an mgo.Session instance that will be traced.
type Session struct {
	*mgo.Session
	cfg mongoConfig
}

func newChildSpanFromContext(config mongoConfig) ddtrace.Span {
	span, _ := tracer.StartSpanFromContext(
		config.ctx,
		"mongodb.query",
		tracer.SpanType(ext.SpanTypeMongoDB),
		tracer.ServiceName(config.serviceName),
		tracer.ResourceName("mongodb.query"))

	for key, value := range config.tags {
		span.SetTag(key, value)
	}

	return span
}

// Run invokes and traces Session.Run
func (s *Session) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.cfg)
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

// Database is an mgo.Database along with the data necessary for tracing.
type Database struct {
	*mgo.Database
	cfg mongoConfig
}

// DB returns a new database for this Session.
func (s *Session) DB(name string) *Database {
	dbCfg := mongoConfig{
		ctx:         s.cfg.ctx,
		serviceName: s.cfg.serviceName,
		tags:        s.cfg.tags,
	}

	dbCfg.tags["database"] = name
	return &Database{
		Database: s.Session.DB(name),
		cfg:      dbCfg,
	}
}

// C returns a new Collection from this Database.
func (db *Database) C(name string) *Collection {
	return &Collection{
		Collection: db.Database.C(name),
		cfg:        db.cfg,
	}
}

// Iter is an mgo.Iter instance that will be traced.
type Iter struct {
	*mgo.Iter

	cfg mongoConfig
}

// Next invokes and traces Iter.Next
func (iter *Iter) Next(result interface{}) bool {
	span := newChildSpanFromContext(iter.cfg)
	r := iter.Iter.Next(result)
	span.Finish()
	return r
}

// For invokes and traces Iter.For
func (iter *Iter) For(result interface{}, f func() error) (err error) {
	span := newChildSpanFromContext(iter.cfg)
	err = iter.Iter.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// All invokes and traces Iter.All
func (iter *Iter) All(result interface{}) (err error) {
	span := newChildSpanFromContext(iter.cfg)
	err = iter.Iter.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Close invokes and traces Iter.Close
func (iter *Iter) Close() (err error) {
	span := newChildSpanFromContext(iter.cfg)
	err = iter.Iter.Close()
	span.Finish(tracer.WithError(err))
	return err
}

// Bulk is an mgo.Bulk instance that will be traced.
type Bulk struct {
	*mgo.Bulk

	cfg mongoConfig
}

// Run invokes and traces Bulk.Run
func (b *Bulk) Run() (result *mgo.BulkResult, err error) {
	span := newChildSpanFromContext(b.cfg)
	result, err = b.Bulk.Run()
	span.Finish(tracer.WithError(err))

	return result, err
}
