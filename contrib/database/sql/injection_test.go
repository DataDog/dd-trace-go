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
			prepared: []string{"/*dde='test-env',ddsn='test-service',ddsv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name: "prepare-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dde='test-env',ddsn='test-service',ddsv='1.0.0'*/ SELECT 1 from DUAL"},
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
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsn='test-service',ddsv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "query-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsid='\\d+',ddsn='test-service',ddsp='1',ddsv='1.0.0',ddtid='1'\\*/ SELECT 1 from DUAL")},
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
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsn='test-service',ddsv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name: "exec-full",
			opts: []RegisterOption{WithSQLCommentInjection(tracer.SQLInjectionModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dde='test-env',ddsid='\\d+',ddsn='test-service',ddsp='1',ddsv='1.0.0',ddtid='1'\\*/ SELECT 1 from DUAL")},
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
				assert.NotContains(t, d.Executed[i], "ddsid='1'")
			}
		})
	}
}
