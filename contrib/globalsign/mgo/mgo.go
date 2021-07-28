// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mgo provides functions and types which allow tracing of the MGO MongoDB client (https://github.com/globalsign/mgo)
package mgo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/globalsign/mgo"

import (
	"math"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/globalsign/mgo"
)

// Dial opens a connection to a MongoDB server and configures it
// for tracing.
func Dial(url string, opts ...DialOption) (*Session, error) {
	session, err := mgo.Dial(url)
	if err != nil {
		return nil, err
	}
	version := "unknown"
	if info, err := session.BuildInfo(); err == nil {
		version = info.Version
	}
	s := &Session{
		Session: session,
		cfg:     newConfig(),
		tags: map[string]string{
			"hosts":       strings.Join(session.LiveServers(), ", "),
			"mgo_version": version,
		},
	}
	for _, fn := range opts {
		fn(s.cfg)
	}
	log.Debug("contrib/globalsign/mgo: Dialing: %s, %#v", url, s.cfg)
	return s, err
}

// Session is an mgo.Session instance that will be traced.
type Session struct {
	*mgo.Session
	cfg  *mongoConfig
	tags map[string]string
}

func newChildSpanFromContext(cfg *mongoConfig, tags map[string]string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMongoDB),
		tracer.ServiceName(cfg.serviceName),
		tracer.ResourceName("mongodb.query"),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(cfg.ctx, "mongodb.query", opts...)
	for key, value := range tags {
		span.SetTag(key, value)
	}
	return span
}

// Run invokes and traces Session.Run
func (s *Session) Run(cmd interface{}, result interface{}) (err error) {
	span := newChildSpanFromContext(s.cfg, s.tags)
	err = s.Session.Run(cmd, result)
	span.Finish(tracer.WithError(err))
	return
}

// Database is an mgo.Database along with the data necessary for tracing.
type Database struct {
	*mgo.Database
	cfg  *mongoConfig
	tags map[string]string
}

// DB returns a new database for this Session.
func (s *Session) DB(name string) *Database {
	tags := make(map[string]string, len(s.tags)+1)
	for k, v := range s.tags {
		tags[k] = v
	}
	tags["name"] = name
	return &Database{
		Database: s.Session.DB(name),
		cfg:      s.cfg,
		tags:     tags,
	}
}

// C returns a new Collection from this Database.
func (db *Database) C(name string) *Collection {
	return &Collection{
		Collection: db.Database.C(name),
		cfg:        db.cfg,
		tags:       db.tags,
	}
}

// Iter is an mgo.Iter instance that will be traced.
type Iter struct {
	*mgo.Iter
	cfg  *mongoConfig
	tags map[string]string
}

// Next invokes and traces Iter.Next
func (iter *Iter) Next(result interface{}) bool {
	span := newChildSpanFromContext(iter.cfg, iter.tags)
	r := iter.Iter.Next(result)
	span.Finish()
	return r
}

// For invokes and traces Iter.For
func (iter *Iter) For(result interface{}, f func() error) (err error) {
	span := newChildSpanFromContext(iter.cfg, iter.tags)
	err = iter.Iter.For(result, f)
	span.Finish(tracer.WithError(err))
	return err
}

// All invokes and traces Iter.All
func (iter *Iter) All(result interface{}) (err error) {
	span := newChildSpanFromContext(iter.cfg, iter.tags)
	err = iter.Iter.All(result)
	span.Finish(tracer.WithError(err))
	return err
}

// Close invokes and traces Iter.Close
func (iter *Iter) Close() (err error) {
	span := newChildSpanFromContext(iter.cfg, iter.tags)
	err = iter.Iter.Close()
	span.Finish(tracer.WithError(err))
	return err
}

// Bulk is an mgo.Bulk instance that will be traced.
type Bulk struct {
	*mgo.Bulk
	tags map[string]string
	cfg  *mongoConfig
}

// Run invokes and traces Bulk.Run
func (b *Bulk) Run() (result *mgo.BulkResult, err error) {
	span := newChildSpanFromContext(b.cfg, b.tags)
	result, err = b.Bulk.Run()
	span.Finish(tracer.WithError(err))

	return result, err
}
