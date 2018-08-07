// Package leveldb provides functions to trace the syndtr/goleveldb package (https://github.com/syndtr/goleveldb).
package leveldb

import (
	"context"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/storage"
	"github.com/syndtr/goleveldb/leveldb/util"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Batch aliases leveldb.Batch so its easier to import.
type Batch = leveldb.Batch

// A DB wraps a leveldb.DB and traces all queries.
type DB struct {
	*leveldb.DB
	cfg *config
}

// Open calls leveldb.Open and wraps the resulting DB.
func Open(stor storage.Storage, o *opt.Options, opts ...Option) (*DB, error) {
	db, err := leveldb.Open(stor, o)
	if err != nil {
		return nil, err
	}
	return WrapDB(db, opts...), nil
}

// OpenFile calls leveldb.OpenFile and wraps the resulting DB.
func OpenFile(path string, o *opt.Options, opts ...Option) (*DB, error) {
	db, err := leveldb.OpenFile(path, o)
	if err != nil {
		return nil, err
	}
	return WrapDB(db, opts...), nil
}

// WrapDB wraps a leveldb.DB so that queries are traced.
func WrapDB(db *leveldb.DB, opts ...Option) *DB {
	return &DB{
		DB:  db,
		cfg: newConfig(opts...),
	}
}

// WithContext returns a new DB with the context set to ctx.
func (db *DB) WithContext(ctx context.Context) *DB {
	newcfg := new(config)
	*newcfg = *db.cfg
	newcfg.ctx = ctx
	return &DB{
		DB:  db.DB,
		cfg: newcfg,
	}
}

// CompactRange calls DB.CompactRange and traces the result.
func (db *DB) CompactRange(r util.Range) error {
	span := startSpan(db.cfg, "CompactRange")
	err := db.DB.CompactRange(r)
	span.Finish(tracer.WithError(err))
	return err
}

// Delete calls DB.Delete and traces the result.
func (db *DB) Delete(key []byte, wo *opt.WriteOptions) error {
	span := startSpan(db.cfg, "Delete")
	err := db.DB.Delete(key, wo)
	span.Finish(tracer.WithError(err))
	return err
}

// Get calls DB.Get and traces the result.
func (db *DB) Get(key []byte, ro *opt.ReadOptions) (value []byte, err error) {
	span := startSpan(db.cfg, "Get")
	value, err = db.DB.Get(key, ro)
	span.Finish(tracer.WithError(err))
	return value, err
}

// GetSnapshot calls DB.GetSnapshot and returns a wrapped Snapshot.
func (db *DB) GetSnapshot() (*Snapshot, error) {
	snap, err := db.DB.GetSnapshot()
	if err != nil {
		return nil, err
	}
	return WrapSnapshot(snap, func(cfg *config) {
		*cfg = *db.cfg
	}), nil
}

// Has calls DB.Has and traces the result.
func (db *DB) Has(key []byte, ro *opt.ReadOptions) (ret bool, err error) {
	span := startSpan(db.cfg, "Has")
	ret, err = db.DB.Has(key, ro)
	span.Finish(tracer.WithError(err))
	return ret, err
}

// NewIterator calls DB.NewIterator and returns a wrapped Iterator.
func (db *DB) NewIterator(slice *util.Range, ro *opt.ReadOptions) iterator.Iterator {
	return WrapIterator(db.DB.NewIterator(slice, ro), func(cfg *config) {
		*cfg = *db.cfg
	})
}

// OpenTransaction calls DB.OpenTransaction and returns a wrapped Transaction.
func (db *DB) OpenTransaction() (*Transaction, error) {
	tr, err := db.DB.OpenTransaction()
	if err != nil {
		return nil, err
	}
	return WrapTransaction(tr, func(cfg *config) {
		*cfg = *db.cfg
	}), nil
}

// Put calls DB.Put and traces the result.
func (db *DB) Put(key, value []byte, wo *opt.WriteOptions) error {
	span := startSpan(db.cfg, "Put")
	err := db.DB.Put(key, value, wo)
	span.Finish(tracer.WithError(err))
	return err
}

// Write calls DB.Write and traces the result.
func (db *DB) Write(batch *Batch, wo *opt.WriteOptions) error {
	span := startSpan(db.cfg, "Write")
	err := db.DB.Write(batch, wo)
	span.Finish(tracer.WithError(err))
	return err
}

// A Snapshot wraps a leveldb.Snapshot and traces all queries.
type Snapshot struct {
	*leveldb.Snapshot
	cfg *config
}

// WrapSnapshot wraps a leveldb.Snapshot so that queries are traced.
func WrapSnapshot(snap *leveldb.Snapshot, opts ...Option) *Snapshot {
	return &Snapshot{
		Snapshot: snap,
		cfg:      newConfig(opts...),
	}
}

// WithContext returns a new Snapshot with the context set to ctx.
func (snap *Snapshot) WithContext(ctx context.Context) *Snapshot {
	newcfg := new(config)
	*newcfg = *snap.cfg
	newcfg.ctx = ctx
	return &Snapshot{
		Snapshot: snap.Snapshot,
		cfg:      newcfg,
	}
}

// Get calls Snapshot.Get and traces the result.
func (snap *Snapshot) Get(key []byte, ro *opt.ReadOptions) (value []byte, err error) {
	span := startSpan(snap.cfg, "Get")
	value, err = snap.Snapshot.Get(key, ro)
	span.Finish(tracer.WithError(err))
	return value, err
}

// Has calls Snapshot.Has and traces the result.
func (snap *Snapshot) Has(key []byte, ro *opt.ReadOptions) (ret bool, err error) {
	span := startSpan(snap.cfg, "Has")
	ret, err = snap.Snapshot.Has(key, ro)
	span.Finish(tracer.WithError(err))
	return ret, err
}

// NewIterator calls Snapshot.NewIterator and returns a wrapped Iterator.
func (snap *Snapshot) NewIterator(slice *util.Range, ro *opt.ReadOptions) iterator.Iterator {
	return WrapIterator(snap.Snapshot.NewIterator(slice, ro), func(cfg *config) {
		*cfg = *snap.cfg
	})
}

// A Transaction wraps a leveldb.Transaction and traces all queries.
type Transaction struct {
	*leveldb.Transaction
	cfg *config
}

// WrapTransaction wraps a leveldb.Transaction so that queries are traced.
func WrapTransaction(tr *leveldb.Transaction, opts ...Option) *Transaction {
	return &Transaction{
		Transaction: tr,
		cfg:         newConfig(opts...),
	}
}

// WithContext returns a new Transaction with the context set to ctx.
func (tr *Transaction) WithContext(ctx context.Context) *Transaction {
	newcfg := new(config)
	*newcfg = *tr.cfg
	newcfg.ctx = ctx
	return &Transaction{
		Transaction: tr.Transaction,
		cfg:         newcfg,
	}
}

// Commit calls Transaction.Commit and traces the result.
func (tr *Transaction) Commit() error {
	span := startSpan(tr.cfg, "Commit")
	err := tr.Transaction.Commit()
	span.Finish(tracer.WithError(err))
	return err
}

// Get calls Transaction.Get and traces the result.
func (tr *Transaction) Get(key []byte, ro *opt.ReadOptions) ([]byte, error) {
	span := startSpan(tr.cfg, "Get")
	value, err := tr.Transaction.Get(key, ro)
	span.Finish(tracer.WithError(err))
	return value, err
}

// Has calls Transaction.Has and traces the result.
func (tr *Transaction) Has(key []byte, ro *opt.ReadOptions) (bool, error) {
	span := startSpan(tr.cfg, "Has")
	ret, err := tr.Transaction.Has(key, ro)
	span.Finish(tracer.WithError(err))
	return ret, err
}

// NewIterator calls Transaction.NewIterator and returns a wrapped Iterator.
func (tr *Transaction) NewIterator(slice *util.Range, ro *opt.ReadOptions) iterator.Iterator {
	return WrapIterator(tr.Transaction.NewIterator(slice, ro), func(cfg *config) {
		*cfg = *tr.cfg
	})
}

// An Iterator wraps a leveldb.Iterator and traces until Release is called.
type Iterator struct {
	iterator.Iterator
	span ddtrace.Span
}

// WrapIterator wraps a leveldb.Iterator so that queries are traced.
func WrapIterator(it iterator.Iterator, opts ...Option) *Iterator {
	return &Iterator{
		Iterator: it,
		span:     startSpan(newConfig(opts...), "Iterator"),
	}
}

// Release calls Iterator.Release and traces the result.
func (it *Iterator) Release() {
	err := it.Error()
	it.Iterator.Release()
	it.span.Finish(tracer.WithError(err))
}

func startSpan(cfg *config, name string) ddtrace.Span {
	span, _ := tracer.StartSpanFromContext(cfg.ctx, "leveldb.query",
		tracer.SpanType(ext.SpanTypeLevelDB),
		tracer.ServiceName(cfg.serviceName),
		tracer.ResourceName(name),
	)
	return span
}
