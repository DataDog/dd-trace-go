// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package topology

import (
	"context"
	"errors"
	"fmt"

	"github.com/mongodb/mongo-go-driver/bson"
	"github.com/mongodb/mongo-go-driver/bson/bsoncodec"
	"github.com/mongodb/mongo-go-driver/core/command"
	"github.com/mongodb/mongo-go-driver/core/option"
	"github.com/mongodb/mongo-go-driver/core/session"
)

type cursor struct {
	clientSession *session.Client
	clock         *session.ClusterClock
	namespace     command.Namespace
	current       int
	batch         *bson.Array
	id            int64
	err           error
	server        *Server
	opts          []option.CursorOptioner
	registry      *bsoncodec.Registry
}

func newCursor(result bson.Reader, clientSession *session.Client, clock *session.ClusterClock, server *Server, opts ...option.CursorOptioner) (command.Cursor, error) {
	cur, err := result.Lookup("cursor")
	if err != nil {
		return nil, err
	}
	if cur.Value().Type() != bson.TypeEmbeddedDocument {
		return nil, fmt.Errorf("cursor should be an embedded document but it is a BSON %s", cur.Value().Type())
	}

	itr, err := cur.Value().ReaderDocument().Iterator()
	if err != nil {
		return nil, err
	}
	var elem *bson.Element
	c := &cursor{
		clientSession: clientSession,
		clock:         clock,
		current:       -1,
		server:        server,
		registry:      server.cfg.registry,
		opts:          opts,
	}
	var ok bool
	for itr.Next() {
		elem = itr.Element()
		switch elem.Key() {
		case "firstBatch":
			c.batch, ok = elem.Value().MutableArrayOK()
			if !ok {
				return nil, fmt.Errorf("firstBatch should be an array but it is a BSON %s", elem.Value().Type())
			}
		case "ns":
			if elem.Value().Type() != bson.TypeString {
				return nil, fmt.Errorf("namespace should be a string but it is a BSON %s", elem.Value().Type())
			}
			namespace := command.ParseNamespace(elem.Value().StringValue())
			err = namespace.Validate()
			if err != nil {
				return nil, err
			}
			c.namespace = namespace
		case "id":
			c.id, ok = elem.Value().Int64OK()
			if !ok {
				return nil, fmt.Errorf("id should be an int64 but it is a BSON %s", elem.Value().Type())
			}
		}
	}

	// close session if everything fits in first batch
	if c.id == 0 {
		c.closeImplicitSession()
	}
	return c, nil
}

// close the associated session if it's implicit
func (c *cursor) closeImplicitSession() {
	if c.clientSession != nil && c.clientSession.SessionType == session.Implicit {
		c.clientSession.EndSession()
	}
}

func (c *cursor) ID() int64 {
	return c.id
}

func (c *cursor) Next(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	c.current++
	if c.current < c.batch.Len() {
		return true
	}

	c.getMore(ctx)

	// call the getMore command in a loop until at least one document is returned in the next batch
	for c.batch.Len() == 0 {
		if c.err != nil || (c.id == 0 && c.batch.Len() == 0) {
			return false
		}

		c.getMore(ctx)
	}

	return true
}

func (c *cursor) Decode(v interface{}) error {
	br, err := c.DecodeBytes()
	if err != nil {
		return err
	}

	return bson.UnmarshalWithRegistry(c.registry, br, v)
}

func (c *cursor) DecodeBytes() (bson.Reader, error) {
	br, err := c.batch.Lookup(uint(c.current))
	if err != nil {
		return nil, err
	}
	if br.Type() != bson.TypeEmbeddedDocument {
		return nil, errors.New("Non-Document in batch of documents for cursor")
	}
	return br.ReaderDocument(), nil
}

func (c *cursor) Err() error {
	return c.err
}

func (c *cursor) Close(ctx context.Context) error {
	defer c.closeImplicitSession()
	conn, err := c.server.Connection(ctx)
	if err != nil {
		return err
	}

	_, err = (&command.KillCursors{
		Clock: c.clock,
		NS:    c.namespace,
		IDs:   []int64{c.id},
	}).RoundTrip(ctx, c.server.SelectedDescription(), conn)
	if err != nil {
		_ = conn.Close() // The command response error is more important here
		return err
	}

	c.id = 0
	return conn.Close()
}

func (c *cursor) getMore(ctx context.Context) {
	c.batch.Reset()
	c.current = 0

	if c.id == 0 {
		return
	}

	conn, err := c.server.Connection(ctx)
	if err != nil {
		c.err = err
		return
	}

	response, err := (&command.GetMore{
		Clock:   c.clock,
		ID:      c.id,
		NS:      c.namespace,
		Opts:    c.opts,
		Session: c.clientSession,
	}).RoundTrip(ctx, c.server.SelectedDescription(), conn)
	if err != nil {
		_ = conn.Close() // The command response error is more important here
		c.err = err
		return
	}

	err = conn.Close()
	if err != nil {
		c.err = err
		return
	}

	id, err := response.Lookup("cursor", "id")
	if err != nil {
		c.err = err
		return
	}
	var ok bool
	c.id, ok = id.Value().Int64OK()
	if !ok {
		c.err = fmt.Errorf("BSON Type %s is not %s", id.Value().Type(), bson.TypeInt64)
		return
	}

	// if this is the last getMore, close the session
	if c.id == 0 {
		c.closeImplicitSession()
	}

	batch, err := response.Lookup("cursor", "nextBatch")
	if err != nil {
		c.err = err
		return
	}
	c.batch, ok = batch.Value().MutableArrayOK()
	if !ok {
		c.err = fmt.Errorf("BSON Type %s is not %s", batch.Value().Type(), bson.TypeArray)
		return
	}

	return
}
