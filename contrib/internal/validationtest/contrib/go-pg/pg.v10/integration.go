// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package pg

import (
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pgtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-pg/pg.v10"

	"github.com/go-pg/pg/v10"
)

type Integration struct {
	conn     *pg.DB
	numSpans int
	opts     []pgtrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]pgtrace.Option, 0),
	}
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, pgtrace.WithServiceName(name))
}

func (i *Integration) Name() string {
	return "go-pg/pg.v10"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	i.conn = pg.Connect(&pg.Options{
		User:     "postgres",
		Password: "postgres",
		Database: "postgres",
	})

	// Wrap the connection with the APM hook.
	pgtrace.Wrap(i.conn, i.opts...)
	var n int
	_, err := i.conn.QueryOne(pg.Scan(&n), "SELECT 1")
	if err != nil {
		log.Fatal(err)
	}
	i.numSpans++

	t.Cleanup(func() {
		i.conn.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	var n int
	res, err := i.conn.QueryOne(pg.Scan(&n), "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, 1, res.RowsAffected())
	i.numSpans++

	var x int
	_, err = i.conn.QueryOne(pg.Scan(&x), "SELECT 2")
	require.NoError(t, err)
	assert.Equal(t, 1, res.RowsAffected())
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
