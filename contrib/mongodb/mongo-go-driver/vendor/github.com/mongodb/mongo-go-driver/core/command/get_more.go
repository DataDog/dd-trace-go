// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package command

import (
	"context"

	"github.com/mongodb/mongo-go-driver/bson"
	"github.com/mongodb/mongo-go-driver/core/description"
	"github.com/mongodb/mongo-go-driver/core/option"
	"github.com/mongodb/mongo-go-driver/core/session"
	"github.com/mongodb/mongo-go-driver/core/wiremessage"
)

// GetMore represents the getMore command.
//
// The getMore command retrieves additional documents from a cursor.
type GetMore struct {
	ID      int64
	NS      Namespace
	Opts    []option.CursorOptioner
	Clock   *session.ClusterClock
	Session *session.Client

	result bson.Reader
	err    error
}

// Encode will encode this command into a wire message for the given server description.
func (gm *GetMore) Encode(desc description.SelectedServer) (wiremessage.WireMessage, error) {
	cmd, err := gm.encode(desc)
	if err != nil {
		return nil, err
	}

	return cmd.Encode(desc)
}

func (gm *GetMore) encode(desc description.SelectedServer) (*Read, error) {
	cmd := bson.NewDocument(
		bson.EC.Int64("getMore", gm.ID),
		bson.EC.String("collection", gm.NS.Collection),
	)

	var err error

	for _, opt := range gm.Opts {
		switch t := opt.(type) {
		case option.OptMaxAwaitTime:
			err = option.OptMaxTime(t).Option(cmd)
		default:
			err = opt.Option(cmd)
		}
		if err != nil {
			return nil, err
		}
	}

	return &Read{
		Clock:   gm.Clock,
		DB:      gm.NS.DB,
		Command: cmd,
		Session: gm.Session,
	}, nil
}

// Decode will decode the wire message using the provided server description. Errors during decoding
// are deferred until either the Result or Err methods are called.
func (gm *GetMore) Decode(desc description.SelectedServer, wm wiremessage.WireMessage) *GetMore {
	rdr, err := (&Read{}).Decode(desc, wm).Result()
	if err != nil {
		gm.err = err
		return gm
	}

	return gm.decode(desc, rdr)
}

func (gm *GetMore) decode(desc description.SelectedServer, rdr bson.Reader) *GetMore {
	gm.result = rdr
	return gm
}

// Result returns the result of a decoded wire message and server description.
func (gm *GetMore) Result() (bson.Reader, error) {
	if gm.err != nil {
		return nil, gm.err
	}

	return gm.result, nil
}

// Err returns the error set on this command.
func (gm *GetMore) Err() error { return gm.err }

// RoundTrip handles the execution of this command using the provided wiremessage.ReadWriter.
func (gm *GetMore) RoundTrip(ctx context.Context, desc description.SelectedServer, rw wiremessage.ReadWriter) (bson.Reader, error) {
	cmd, err := gm.encode(desc)
	if err != nil {
		return nil, err
	}

	rdr, err := cmd.RoundTrip(ctx, desc, rw)
	if err != nil {
		return nil, err
	}

	return gm.decode(desc, rdr).Result()
}
