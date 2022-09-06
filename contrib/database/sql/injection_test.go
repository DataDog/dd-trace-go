// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestCommentInjection(t *testing.T) {
	testCases := []struct {
		name     string
		opts     []RegisterOption
		callDB   func(ctx context.Context, db *sql.DB) error
		prepared []string
		executed []*regexp.Regexp
	}{
		{
			name: "prepare",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepare-disabled",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepare-service",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "prepare-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "query",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query-disabled",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query-service",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "query-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec-disabled",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec-service",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01'\\*/ SELECT 1 from DUAL")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracer.Start(tracer.WithService("test-service"), tracer.WithEnv("test-env"), tracer.WithServiceVersion("1.0.0"))
			defer tracer.Stop()

			d := &internal.MockDriver{}
			Register("test", d, tc.opts...)
			defer unregister("test")

			db, err := Open("test", "dn")
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			require.Len(t, d.Prepared, len(tc.prepared))
			for i, e := range tc.prepared {
				assert.Equal(t, e, d.Prepared[i])
			}

			require.Len(t, d.Executed, len(tc.executed))
			for i, e := range tc.executed {
				assert.Regexp(t, e, d.Executed[i])
				// the injected span ID should not be the parent's span ID
				assert.NotContains(t, d.Executed[i], "traceparent='00-00000000000000000000000000000001-0000000000000001")
			}
		})
	}
}

func TestDBMTraceContextTagging(t *testing.T) {
	testCases := []struct {
		name                    string
		opts                    []RegisterOption
		callDB                  func(ctx context.Context, db *sql.DB) error
		spanType                string
		traceContextInjectedTag bool
	}{
		{
			name: "prepare",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypePrepare,
			traceContextInjectedTag: false,
		},
		{
			name: "query-disabled",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-service",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeQuery,
			traceContextInjectedTag: true,
		},
		{
			name: "exec-disabled",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-service",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                queryTypeExec,
			traceContextInjectedTag: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tr := mocktracer.Start()
			defer tr.Stop()

			d := &internal.MockDriver{}
			Register("test", d, tc.opts...)
			defer unregister("test")

			db, err := Open("test", "dn")
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			spans := tr.FinishedSpans()

			sps := spansOfType(spans, tc.spanType)
			for _, s := range sps {
				tags := s.Tags()
				if tc.traceContextInjectedTag {
					assert.Equal(t, tags[keyDBMTraceInjected], true)
				} else {
					_, ok := tags[keyDBMTraceInjected]
					assert.False(t, ok)
				}
			}
		})
	}
}

func spansOfType(spans []mocktracer.Span, spanType string) (filtered []mocktracer.Span) {
	filtered = make([]mocktracer.Span, 0)
	for _, s := range spans {
		if s.Tag("sql.query_type") == spanType {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
