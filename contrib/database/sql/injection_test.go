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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestCommentInjection(t *testing.T) {
	testCases := []struct {
		name                  string
		opts                  []RegisterOption
		callDB                func(ctx context.Context, db *sql.DB) error
		expectedPreparedStmts []string
		expectedExecutedStmts []*regexp.Regexp
	}{
		{
			name: "prepared statement with default mode (disabled)",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedPreparedStmts: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepared statement in explicitly disabled mode",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedPreparedStmts: []string{"SELECT 1 from DUAL"},
		},
		{
			name: "prepared statement in service tags only mode",
			opts: []RegisterOption{WithCommentInjection(tracer.ServiceTagsInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedPreparedStmts: []string{"/*dde='test-env',ddsn='test-service',ddsv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "prepared statement in full mode",
			opts: []RegisterOption{WithCommentInjection(tracer.FullSQLCommentInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedPreparedStmts: []string{"/*dde='test-env',ddsn='test-service',ddsv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "query in default mode (disabled)",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query in explicitly disabled mode",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "query in service tags only mode",
			opts: []RegisterOption{WithCommentInjection(tracer.ServiceTagsInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsn='test-service',ddsv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "query in full mode",
			opts: []RegisterOption{WithCommentInjection(tracer.FullSQLCommentInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsid='[0-9]+',ddsn='test-service',ddsp='1',ddsv='1.0.0',ddtid='1'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec in default mode (disabled)",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec in explicitly disabled mode",
			opts: []RegisterOption{WithCommentInjection(tracer.CommentInjectionDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name: "exec in service tags only mode",
			opts: []RegisterOption{WithCommentInjection(tracer.ServiceTagsInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsn='test-service',ddsv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec in full mode",
			opts: []RegisterOption{WithCommentInjection(tracer.FullSQLCommentInjection)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			expectedExecutedStmts: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsid='[0-9]+',ddsn='test-service',ddsp='1',ddsv='1.0.0',ddtid='1'\\*/ SELECT 1 from DUAL")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracer.Start(tracer.WithService("test-service"), tracer.WithEnv("test-env"), tracer.WithServiceVersion("1.0.0"))
			defer tracer.Stop()

			d := internal.NewMockDriver()
			Register("test", d, tc.opts...)
			defer unregister("test")

			db, err := Open("test", "dn")
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			require.Len(t, d.PreparedStmts, len(tc.expectedPreparedStmts))
			for i, e := range tc.expectedPreparedStmts {
				assert.Equal(t, e, d.PreparedStmts[i])
			}

			require.Len(t, d.ExecutedQueries, len(tc.expectedExecutedStmts))
			for i, e := range tc.expectedExecutedStmts {
				assert.Regexp(t, e, d.ExecutedQueries[i])
			}
		})
	}
}
