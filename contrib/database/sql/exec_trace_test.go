// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// TODO: skip if Go >= 1.21, until gotraceui handles those format changes?

package sql

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"runtime/trace"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"

	"github.com/stretchr/testify/require"
	gotraceui "honnef.co/go/gotraceui/trace"
)

func TestExecutionTraceAnnotations(t *testing.T) {
	if trace.IsEnabled() {
		t.Skip("execution tracing is already enabled")
	}

	// In-memory server & client which discards everything, to avoid
	// slowness from unnecessary network I/O
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	tracer.Start(tracer.WithHTTPClient(c), tracer.WithLogStartup(false))

	buf := new(bytes.Buffer)
	require.NoError(t, trace.Start(buf), "starting execution tracing")

	// sleepDuration is the amount of time our mock DB operations should
	// take. We are going to assert that the execution trace tasks
	// corresponding with the duration are at least as long as this. In
	// reality they could be longer than this due to slow CI, scheduling
	// jitter, etc., but we know that they should be at least this long.
	const sleepDuration = 10 * time.Millisecond

	Register("mock", &internal.MockDriver{
		Hook: func() {
			time.Sleep(sleepDuration)
		},
	})
	db, err := Open("mock", "")
	require.NoError(t, err, "opening mock db")

	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	_, err = db.ExecContext(ctx, "foobar")
	require.NoError(t, err, "executing mock statement")
	span.Finish()

	trace.Stop()
	tracer.Stop()

	tasks, err := tasksFromTrace(buf)
	require.NoError(t, err, "getting tasks from trace")

	// We expect 3 tasks:
	//	* The parent span
	//	* A span for connecting to the mock database
	//	* A span for executing the mock query
	// The database spans should take at least sleepDuration and be
	// connected to the parent.

	var parent, connect, execute int
	for id, task := range tasks {
		t.Logf("task %d: %+v", id, task)
		switch task.name {
		case "parent":
			parent = id
		case "Connect":
			connect = id
		case "Exec":
			execute = id
		}
	}

	require.NotZero(t, parent)
	require.NotZero(t, connect)
	require.NotZero(t, execute)
	require.Equal(t, parent, tasks[connect].parent)
	require.Equal(t, parent, tasks[execute].parent)
	require.GreaterOrEqual(t, tasks[parent].duration, tasks[connect].duration+tasks[execute].duration)
	require.GreaterOrEqual(t, tasks[connect].duration, sleepDuration)
	require.GreaterOrEqual(t, tasks[execute].duration, sleepDuration)
}

type traceTask struct {
	name     string
	duration time.Duration
	parent   int
	// logs?
}

func tasksFromTrace(r io.Reader) (map[int]traceTask, error) {
	execTrace, err := gotraceui.Parse(r, nil)
	if err != nil {
		return nil, err
	}

	tasks := make(map[int]traceTask)
	for _, ev := range execTrace.Events {
		if ev.Type != gotraceui.EvUserTaskCreate || ev.Link == -1 {
			continue
		}
		id := int(ev.Args[0])
		parent := int(ev.Args[1])
		name := execTrace.Strings[ev.Args[2]]
		tasks[id] = traceTask{
			name:     name,
			parent:   parent,
			duration: time.Duration(execTrace.Events[ev.Link].Ts - ev.Ts),
		}
	}
	return tasks, nil
}
