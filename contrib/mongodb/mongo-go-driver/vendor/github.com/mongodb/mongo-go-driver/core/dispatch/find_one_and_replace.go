// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package dispatch

import (
	"context"

	"github.com/mongodb/mongo-go-driver/core/command"
	"github.com/mongodb/mongo-go-driver/core/description"
	"github.com/mongodb/mongo-go-driver/core/result"
	"github.com/mongodb/mongo-go-driver/core/session"
	"github.com/mongodb/mongo-go-driver/core/topology"
	"github.com/mongodb/mongo-go-driver/core/uuid"
	"github.com/mongodb/mongo-go-driver/core/writeconcern"
)

// FindOneAndReplace handles the full cycle dispatch and execution of a FindOneAndReplace command against the provided
// topology.
func FindOneAndReplace(
	ctx context.Context,
	cmd command.FindOneAndReplace,
	topo *topology.Topology,
	selector description.ServerSelector,
	clientID uuid.UUID,
	pool *session.Pool,
	retryWrite bool,
) (result.FindAndModify, error) {

	ss, err := topo.SelectServer(ctx, selector)
	if err != nil {
		return result.FindAndModify{}, err
	}

	// If no explicit session and deployment supports sessions, start implicit session.
	if cmd.Session == nil && topo.SupportsSessions() {
		cmd.Session, err = session.NewClientSession(pool, clientID, session.Implicit)
		if err != nil {
			return result.FindAndModify{}, err
		}
		defer cmd.Session.EndSession()
	}

	// Execute in a single trip if retry writes not supported, or retry not enabled
	if !retrySupported(topo, ss.Description(), cmd.Session, cmd.WriteConcern) || !retryWrite {
		if cmd.Session != nil {
			cmd.Session.RetryWrite = false // explicitly set to false to prevent encoding transaction number
		}
		return findOneAndReplace(ctx, cmd, ss, nil)
	}

	cmd.Session.RetryWrite = retryWrite
	cmd.Session.IncrementTxnNumber()

	res, originalErr := findOneAndReplace(ctx, cmd, ss, nil)

	// Retry if appropriate
	if cerr, ok := originalErr.(command.Error); ok && cerr.Retryable() {
		ss, err := topo.SelectServer(ctx, selector)

		// Return original error if server selection fails or new server does not support retryable writes
		if err != nil || !retrySupported(topo, ss.Description(), cmd.Session, cmd.WriteConcern) {
			return result.FindAndModify{}, originalErr
		}

		return findOneAndReplace(ctx, cmd, ss, cerr)
	}

	return res, originalErr
}

func findOneAndReplace(
	ctx context.Context,
	cmd command.FindOneAndReplace,
	ss *topology.SelectedServer,
	oldErr error,
) (result.FindAndModify, error) {
	desc := ss.Description()
	conn, err := ss.Connection(ctx)
	if err != nil {
		if oldErr != nil {
			return result.FindAndModify{}, oldErr
		}
		return result.FindAndModify{}, err
	}

	if !writeconcern.AckWrite(cmd.WriteConcern) {
		go func() {
			defer func() { _ = recover() }()
			defer conn.Close()

			_, _ = cmd.RoundTrip(ctx, desc, conn)
		}()

		return result.FindAndModify{}, command.ErrUnacknowledgedWrite
	}
	defer conn.Close()

	return cmd.RoundTrip(ctx, desc, conn)
}
