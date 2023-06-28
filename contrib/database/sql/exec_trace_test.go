// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// TODO: gotraceui does not currently handle Go 1.21 execution tracer changes,
// so we need to skip this test for that version. We still have coverage for
// older Go versions due to our support policy, and Go 1.21 shouldn't fundamentally
// change the behavior this test is covering. Remove this build constraint
// once gotraceui supports Go 1.21
//
//go:build !go1.21

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

	"github.com/stretchr/testify/assert"
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
	defer tracer.Stop()

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
	conn, err := db.Conn(ctx)
	require.NoError(t, err, "connecting to DB")
	rows, err := conn.QueryContext(ctx, "foobar")
	require.NoError(t, err, "executing mock query")
	rows.Close()
	stmt, err := conn.PrepareContext(ctx, "prepared")
	require.NoError(t, err, "preparing mock statement")
	_, err = stmt.Exec()
	require.NoError(t, err, "executing mock perpared statement")
	rows, err = stmt.Query()
	require.NoError(t, err, "executing mock perpared query")
	rows.Close()
	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err, "beginning mock transaction")
	_, err = tx.ExecContext(ctx, "foobar")
	require.NoError(t, err, "executing query in mock transaction")
	require.NoError(t, tx.Commit(), "commiting mock transaction")
	require.NoError(t, conn.Close())

	span.Finish()

	trace.Stop()

	tasks, err := tasksFromTrace(buf)
	require.NoError(t, err, "getting tasks from trace")

	expectedParentChildTasks := []string{"Connect", "Exec", "Query", "Prepare", "Begin", "Exec"}
	expectedPreparedStatementTasks := []string{"Exec", "Query"}

	var foundParent, foundPrepared bool
	for id, task := range tasks {
		t.Logf("task %d: %+v", id, task)
		switch task.name {
		case "parent":
			foundParent = true
			var gotParentChildTasks []string
			for _, id := range tasks[id].children {
				gotParentChildTasks = append(gotParentChildTasks, tasks[id].name)
			}
			assert.ElementsMatch(t, expectedParentChildTasks, gotParentChildTasks)
		case "Prepare":
			foundPrepared = true
			var gotPerparedStatementTasks []string
			for _, id := range tasks[id].children {
				gotPerparedStatementTasks = append(gotPerparedStatementTasks, tasks[id].name)
			}
			assert.ElementsMatch(t, expectedPreparedStatementTasks, gotPerparedStatementTasks)
			assert.GreaterOrEqual(t, task.duration, sleepDuration, "task %s", task.name)
		case "Connect", "Exec", "Begin", "Commit", "Query":
			assert.GreaterOrEqual(t, task.duration, sleepDuration, "task %s", task.name)
		default:
			continue
		}
	}
	assert.True(t, foundParent, "need parent task")
	assert.True(t, foundPrepared, "need prepared statement task")
}

type traceTask struct {
	name     string
	duration time.Duration
	parent   int
	children []int
}

func tasksFromTrace(r io.Reader) (map[int]traceTask, error) {
	execTrace, err := gotraceui.Parse(r, nil)
	if err != nil {
		return nil, err
	}

	tasks := make(map[int]traceTask)
	for _, ev := range execTrace.Events {
		switch ev.Type {
		case gotraceui.EvUserTaskCreate:
			if ev.Link == -1 {
				continue
			}
			id := int(ev.Args[0])
			parent := int(ev.Args[1])
			if parent != 0 {
				t := tasks[parent]
				t.children = append(t.children, id)
				tasks[parent] = t
			}
			name := execTrace.Strings[ev.Args[2]]
			tasks[id] = traceTask{
				name:     name,
				parent:   parent,
				duration: time.Duration(execTrace.Events[ev.Link].Ts - ev.Ts),
			}
		}
	}
	return tasks, nil
}
