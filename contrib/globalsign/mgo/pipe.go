package mgo

import (
	"github.com/globalsign/mgo"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Pipe is an mgo.Pipe instance along with the data necessary for tracing.
type Pipe struct {
	*mgo.Pipe
	cfg mongoConfig
}

// Iter invokes and traces Pipe.Iter
func (p *Pipe) Iter() *Iter {
	span := newChildSpanFromContext(p.cfg)
	iter := p.Pipe.Iter()
	span.Finish()
	return &Iter{
		Iter: iter,
		cfg:  p.cfg,
	}
}

// All invokes and traces Pipe.All
func (p *Pipe) All(result interface{}) error {
	return p.Iter().All(result)
}

// One invokes and traces Pipe.One
func (p *Pipe) One(result interface{}) (err error) {
	span := newChildSpanFromContext(p.cfg)
	defer span.Finish(tracer.WithError(err))
	err = p.Pipe.One(result)
	return
}

// AllowDiskUse invokes and traces Pipe.AllowDiskUse
func (p *Pipe) AllowDiskUse() *Pipe {
	return &Pipe{
		Pipe: p.Pipe.AllowDiskUse(),
		cfg:  p.cfg,
	}
}

// Batch invokes and traces Pipe.Batch
func (p *Pipe) Batch(n int) *Pipe {
	return &Pipe{
		Pipe: p.Pipe.Batch(n),
		cfg:  p.cfg,
	}
}

// Explain invokes and traces Pipe.Explain
func (p *Pipe) Explain(result interface{}) (err error) {
	span := newChildSpanFromContext(p.cfg)
	defer span.Finish(tracer.WithError(err))
	err = p.Pipe.Explain(result)
	return
}
