// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package buntdb

import (
	"context"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/tidwall/buntdb"
)

// A DB wraps a buntdb.DB, automatically tracing any transactions.
type DB struct {
	*buntdb.DB
	opts []Option
	ctx  context.Context
}

// Open calls buntdb.Open and wraps the result.
func Open(path string, opts ...Option) (*DB, error) {
	db, err := buntdb.Open(path)
	if err != nil {
		return nil, err
	}
	return WrapDB(db, opts...), nil
}

// WrapDB wraps a buntdb.DB so it can be traced.
func WrapDB(db *buntdb.DB, opts ...Option) *DB {
	return &DB{
		DB:   db,
		opts: opts,
		ctx:  context.Background(),
	}
}

// Begin calls the underlying DB.Begin and traces the transaction.
func (db *DB) Begin(writable bool) (*Tx, error) {
	tx, err := db.DB.Begin(writable)
	if err != nil {
		return nil, err
	}
	return WrapTx(tx, db.opts...), nil
}

// Update calls the underlying DB.Update and traces the transaction.
func (db *DB) Update(fn func(tx *Tx) error) error {
	return db.DB.Update(func(tx *buntdb.Tx) error {
		return fn(WrapTx(tx, db.opts...))
	})
}

// View calls the underlying DB.View and traces the transaction.
func (db *DB) View(fn func(tx *Tx) error) error {
	return db.DB.View(func(tx *buntdb.Tx) error {
		return fn(WrapTx(tx, db.opts...))
	})
}

// WithContext sets the context for the DB.
func (db *DB) WithContext(ctx context.Context) *DB {
	newdb := *db
	newdb.opts = append(newdb.opts[:len(newdb.opts):len(newdb.opts)], WithContext(ctx))
	return &newdb
}

// A Tx wraps a buntdb.Tx, automatically tracing any queries.
type Tx struct {
	*buntdb.Tx
	cfg *config
}

// WrapTx wraps a buntdb.Tx so it can be traced.
func WrapTx(tx *buntdb.Tx, opts ...Option) *Tx {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	return &Tx{
		Tx:  tx,
		cfg: cfg,
	}
}

func (tx *Tx) startSpan(name string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.AppTypeDB),
		tracer.ServiceName(tx.cfg.serviceName),
		tracer.ResourceName(name),
		tracer.MeasureSpan(),
	}
	if !math.IsNaN(tx.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tx.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(tx.cfg.ctx, "buntdb.query", opts...)
	return span
}

// WithContext sets the context for the Tx.
func (tx *Tx) WithContext(ctx context.Context) *Tx {
	newcfg := *tx.cfg
	newcfg.ctx = ctx
	return &Tx{
		Tx:  tx.Tx,
		cfg: &newcfg,
	}
}

// Ascend calls the underlying Tx.Ascend and traces the query.
func (tx *Tx) Ascend(index string, iterator func(key, value string) bool) error {
	span := tx.startSpan("Ascend")
	err := tx.Tx.Ascend(index, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// AscendEqual calls the underlying Tx.AscendEqual and traces the query.
func (tx *Tx) AscendEqual(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("AscendEqual")
	err := tx.Tx.AscendEqual(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// AscendGreaterOrEqual calls the underlying Tx.AscendGreaterOrEqual and traces the query.
func (tx *Tx) AscendGreaterOrEqual(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("AscendGreaterOrEqual")
	err := tx.Tx.AscendGreaterOrEqual(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// AscendKeys calls the underlying Tx.AscendKeys and traces the query.
func (tx *Tx) AscendKeys(pattern string, iterator func(key, value string) bool) error {
	span := tx.startSpan("AscendKeys")
	err := tx.Tx.AscendKeys(pattern, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// AscendLessThan calls the underlying Tx.AscendLessThan and traces the query.
func (tx *Tx) AscendLessThan(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("AscendLessThan")
	err := tx.Tx.AscendLessThan(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// AscendRange calls the underlying Tx.AscendRange and traces the query.
func (tx *Tx) AscendRange(index, greaterOrEqual, lessThan string, iterator func(key, value string) bool) error {
	span := tx.startSpan("AscendRange")
	err := tx.Tx.AscendRange(index, greaterOrEqual, lessThan, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// CreateIndex calls the underlying Tx.CreateIndex and traces the query.
func (tx *Tx) CreateIndex(name, pattern string, less ...func(a, b string) bool) error {
	span := tx.startSpan("CreateIndex")
	err := tx.Tx.CreateIndex(name, pattern, less...)
	span.Finish(tracer.WithError(err))
	return err
}

// CreateIndexOptions calls the underlying Tx.CreateIndexOptions and traces the query.
func (tx *Tx) CreateIndexOptions(name, pattern string, opts *buntdb.IndexOptions, less ...func(a, b string) bool) error {
	span := tx.startSpan("CreateIndexOptions")
	err := tx.Tx.CreateIndexOptions(name, pattern, opts, less...)
	span.Finish(tracer.WithError(err))
	return err
}

// CreateSpatialIndex calls the underlying Tx.CreateSpatialIndex and traces the query.
func (tx *Tx) CreateSpatialIndex(name, pattern string, rect func(item string) (min, max []float64)) error {
	span := tx.startSpan("CreateSpatialIndex")
	err := tx.Tx.CreateSpatialIndex(name, pattern, rect)
	span.Finish(tracer.WithError(err))
	return err
}

// CreateSpatialIndexOptions calls the underlying Tx.CreateSpatialIndexOptions and traces the query.
func (tx *Tx) CreateSpatialIndexOptions(name, pattern string, opts *buntdb.IndexOptions, rect func(item string) (min, max []float64)) error {
	span := tx.startSpan("CreateSpatialIndexOptions")
	err := tx.Tx.CreateSpatialIndexOptions(name, pattern, opts, rect)
	span.Finish(tracer.WithError(err))
	return err
}

// Delete calls the underlying Tx.Delete and traces the query.
func (tx *Tx) Delete(key string) (val string, err error) {
	span := tx.startSpan("Delete")
	val, err = tx.Tx.Delete(key)
	span.Finish(tracer.WithError(err))
	return val, err
}

// DeleteAll calls the underlying Tx.DeleteAll and traces the query.
func (tx *Tx) DeleteAll() error {
	span := tx.startSpan("DeleteAll")
	err := tx.Tx.DeleteAll()
	span.Finish(tracer.WithError(err))
	return err
}

// Descend calls the underlying Tx.Descend and traces the query.
func (tx *Tx) Descend(index string, iterator func(key, value string) bool) error {
	span := tx.startSpan("Descend")
	err := tx.Tx.Descend(index, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DescendEqual calls the underlying Tx.DescendEqual and traces the query.
func (tx *Tx) DescendEqual(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("DescendEqual")
	err := tx.Tx.DescendEqual(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DescendGreaterThan calls the underlying Tx.DescendGreaterThan and traces the query.
func (tx *Tx) DescendGreaterThan(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("DescendGreaterThan")
	err := tx.Tx.DescendGreaterThan(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DescendKeys calls the underlying Tx.DescendKeys and traces the query.
func (tx *Tx) DescendKeys(pattern string, iterator func(key, value string) bool) error {
	span := tx.startSpan("DescendKeys")
	err := tx.Tx.DescendKeys(pattern, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DescendLessOrEqual calls the underlying Tx.DescendLessOrEqual and traces the query.
func (tx *Tx) DescendLessOrEqual(index, pivot string, iterator func(key, value string) bool) error {
	span := tx.startSpan("DescendLessOrEqual")
	err := tx.Tx.DescendLessOrEqual(index, pivot, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DescendRange calls the underlying Tx.DescendRange and traces the query.
func (tx *Tx) DescendRange(index, lessOrEqual, greaterThan string, iterator func(key, value string) bool) error {
	span := tx.startSpan("DescendRange")
	err := tx.Tx.DescendRange(index, lessOrEqual, greaterThan, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// DropIndex calls the underlying Tx.DropIndex and traces the query.
func (tx *Tx) DropIndex(name string) error {
	span := tx.startSpan("DropIndex")
	err := tx.Tx.DropIndex(name)
	span.Finish(tracer.WithError(err))
	return err
}

// Get calls the underlying Tx.Get and traces the query.
func (tx *Tx) Get(key string, ignoreExpired ...bool) (val string, err error) {
	span := tx.startSpan("Get")
	val, err = tx.Tx.Get(key, ignoreExpired...)
	span.Finish(tracer.WithError(err))
	return val, err
}

// Indexes calls the underlying Tx.Indexes and traces the query.
func (tx *Tx) Indexes() ([]string, error) {
	span := tx.startSpan("Indexes")
	indexes, err := tx.Tx.Indexes()
	span.Finish(tracer.WithError(err))
	return indexes, err
}

// Intersects calls the underlying Tx.Intersects and traces the query.
func (tx *Tx) Intersects(index, bounds string, iterator func(key, value string) bool) error {
	span := tx.startSpan("Intersects")
	err := tx.Tx.Intersects(index, bounds, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// Len calls the underlying Tx.Len and traces the query.
func (tx *Tx) Len() (int, error) {
	span := tx.startSpan("Len")
	n, err := tx.Tx.Len()
	span.Finish(tracer.WithError(err))
	return n, err
}

// Nearby calls the underlying Tx.Nearby and traces the query.
func (tx *Tx) Nearby(index, bounds string, iterator func(key, value string, dist float64) bool) error {
	span := tx.startSpan("Nearby")
	err := tx.Tx.Nearby(index, bounds, iterator)
	span.Finish(tracer.WithError(err))
	return err
}

// Set calls the underlying Tx.Set and traces the query.
func (tx *Tx) Set(key, value string, opts *buntdb.SetOptions) (previousValue string, replaced bool, err error) {
	span := tx.startSpan("Set")
	previousValue, replaced, err = tx.Tx.Set(key, value, opts)
	span.Finish(tracer.WithError(err))
	return previousValue, replaced, err
}

// TTL calls the underlying Tx.TTL and traces the query.
func (tx *Tx) TTL(key string) (time.Duration, error) {
	span := tx.startSpan("TTL")
	duration, err := tx.Tx.TTL(key)
	span.Finish(tracer.WithError(err))
	return duration, err
}
