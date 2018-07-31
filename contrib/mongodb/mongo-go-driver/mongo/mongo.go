package mongo

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mongodb/mongo-go-driver/core/event"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// The values of these mongo fields will not be scrubbed out. This allows the
// non-sensitive collection names to be captured.
var unscrubbedFields = []string{
	"ordered", "insert", "count", "find", "create",
}

type monitor struct {
	sync.Mutex
	spans map[int64]ddtrace.Span
}

func (m *monitor) Started(evt *event.CommandStartedEvent) {
	hostname, port := peerInfo(evt)
	statement := scrub(evt.Command).ToExtJSON(false)

	span, _ := tracer.StartSpanFromContext(evt.Context, "mongodb.query",
		tracer.ServiceName("mongo"),
		tracer.ResourceName("mongo."+evt.CommandName),
		tracer.Tag(ext.DBInstance, evt.DatabaseName),
		tracer.Tag(ext.DBStatement, statement),
		tracer.Tag(ext.DBType, "mongo"),
		tracer.Tag(ext.PeerHostname, hostname),
		tracer.Tag(ext.PeerPort, port),
	)
	m.Lock()
	m.spans[evt.RequestID] = span
	m.Unlock()
}

func (m *monitor) Succeeded(evt *event.CommandSucceededEvent) {
	m.Lock()
	span, ok := m.spans[evt.RequestID]
	if ok {
		delete(m.spans, evt.RequestID)
	}
	m.Unlock()
	if !ok {
		return
	}
	span.Finish()
}

func (m *monitor) Failed(evt *event.CommandFailedEvent) {
	m.Lock()
	span, ok := m.spans[evt.RequestID]
	if ok {
		delete(m.spans, evt.RequestID)
	}
	m.Unlock()
	if !ok {
		return
	}
	span.Finish(tracer.WithError(fmt.Errorf(evt.Failure)))
}

// NewMonitor creates a new mongodb event CommandMonitor.
func NewMonitor() *event.CommandMonitor {
	m := &monitor{
		spans: make(map[int64]ddtrace.Span),
	}
	return &event.CommandMonitor{
		Started:   m.Started,
		Succeeded: m.Succeeded,
		Failed:    m.Failed,
	}
}

func peerInfo(evt *event.CommandStartedEvent) (hostname, port string) {
	hostname = evt.ConnectionID
	port = "27017"
	if idx := strings.IndexByte(hostname, '['); idx >= 0 {
		hostname = hostname[:idx]
	}
	if idx := strings.IndexByte(hostname, ':'); idx >= 0 {
		port = hostname[idx+1:]
		hostname = hostname[:idx]
	}
	return hostname, port
}
