// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package exectracetest

import (
	"context"
	"net/http"
	"runtime/trace"
	"slices"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	exptrace "golang.org/x/exp/trace"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

func must[T any](val T, err error) T {
	if err != nil {
		panic(err)
	}
	return val
}

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

	// sleepDuration is the amount of time our mock DB operations should
	// take. We are going to assert that the execution trace tasks
	// corresponding with the duration are at least as long as this. In
	// reality they could be longer than this due to slow CI, scheduling
	// jitter, etc., but we know that they should be at least this long.
	const sleepDuration = 10 * time.Millisecond
	sleep := func() int {
		time.Sleep(sleepDuration)
		return 0
	}
	sqltrace.Register("sqlite3_extended",
		&sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				time.Sleep(sleepDuration)
				return conn.RegisterFunc("sleep", sleep, true)
			},
		},
	)

	db := must(sqltrace.Open("sqlite3_extended", ":memory:"))

	_, events := collectTestData(t, func() {
		span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		must(db.ExecContext(ctx, "select sleep()"))
		conn := must(db.Conn(ctx))
		rows := must(conn.QueryContext(ctx, "select 1"))
		rows.Close()
		stmt := must(conn.PrepareContext(ctx, "select sleep()"))
		must(stmt.Exec())
		rows = must(stmt.Query())
		// NB: the sleep() is only actually evaluated when
		// we iterate over the rows, and that part isn't traced,
		// so we won't make assertions about the duration of the
		// "Query" task later
		rows.Close()
		tx := must(conn.BeginTx(ctx, nil))
		must(tx.ExecContext(ctx, "select sleep()"))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}

		span.Finish()
	})

	tasks := getTasks(events)

	expectedParentChildTasks := []string{"Connect", "Exec", "Query", "Prepare", "Begin", "Exec"}
	var foundParent, foundPrepared bool
	for id, task := range tasks {
		t.Logf("task %d: %+v", id, task)
		switch task.name {
		case "parent":
			foundParent = true
			var got []string
			for _, child := range tasks[id].children {
				got = append(got, tasks[child].name)
			}
			if !slices.Equal(expectedParentChildTasks, got) {
				t.Errorf(
					"did not find expected child tasks of parent: want %s, got %s",
					expectedParentChildTasks, got,
				)
			}
		case "Prepare":
			foundPrepared = true
		case "Connect", "Exec":
			if d := task.Duration(); d < sleepDuration {
				t.Errorf("task %s: duration %v less than minimum %v", task.name, d, sleepDuration)
			}
		}
	}
	if !foundParent {
		t.Error("did not find parent task")
	}
	if !foundPrepared {
		t.Error("did not find prepared statement task")
	}
}

type traceTask struct {
	name       string
	start, end exptrace.Time
	parent     exptrace.TaskID
	children   []exptrace.TaskID
}

func (t *traceTask) Duration() time.Duration { return time.Duration(t.end - t.start) }

func getTasks(events []exptrace.Event) map[exptrace.TaskID]*traceTask {
	tasks := make(map[exptrace.TaskID]*traceTask)
	for _, ev := range events {
		switch ev.Kind() {
		case exptrace.EventTaskBegin:
			task := ev.Task()
			parent := task.Parent
			if t, ok := tasks[parent]; ok {
				t.children = append(t.children, task.ID)
			}
			tasks[task.ID] = &traceTask{
				name:   task.Type,
				parent: parent,
				start:  ev.Time(),
			}
		case exptrace.EventTaskEnd:
			task := ev.Task()
			if t, ok := tasks[task.ID]; ok {
				t.end = ev.Time()
			}
		default:
		}
	}
	return tasks
}
