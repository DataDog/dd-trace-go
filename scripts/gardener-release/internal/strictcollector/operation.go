// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"context"
	"sync"
	"time"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

const (
	stateV3AssemblySessions           = 3
	stateV3AssemblyStateReads         = 4091
	stateV3AssemblyCoordinationReads  = 3582
	stateV3AssemblyLogicalReads       = stateV3AssemblyStateReads*2 + stateV3AssemblyCoordinationReads
	stateV3AssemblyAttempts           = stateV3AssemblyLogicalReads * maxAttempts
	stateV3AssemblyResponseBytes      = 576 << 20
	stateV3AssemblyChildResponseBytes = stateV3AssemblyResponseBytes / stateV3AssemblySessions
)

type stateV3AssemblyRole uint8

const (
	stateV3AssemblyMinor stateV3AssemblyRole = iota + 1
	stateV3AssemblyPatch
	stateV3AssemblyCoordination
)

type stateV3AssemblyQuota struct {
	mu       sync.Mutex
	attempts int
	bytes    int
}

func (q *stateV3AssemblyQuota) chargeAttempt() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.attempts == 0 {
		return false
	}
	q.attempts--
	return true
}
func (q *stateV3AssemblyQuota) chargeBytes(n int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if n < 0 || n > q.bytes {
		return false
	}
	q.bytes -= n
	return true
}

// stateV3AssemblyChild is an operation-owned, role-bound capability. It owns
// no lease, caller context, deadline, or ref selector: the assigned session
// retains those values until closeAssembly.
type stateV3AssemblyChild struct {
	session *session
	store   *stateV3DocumentStore
}

func (c *stateV3AssemblyChild) readRoot() (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyRoot()
}
func (c *stateV3AssemblyChild) readRawCommitForRoot(prior handle) (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyRawForRef(prior)
}
func (c *stateV3AssemblyChild) readRawCommitParent(prior handle) (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyRawParent(prior)
}
func (c *stateV3AssemblyChild) readRESTCommit(prior handle) (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyREST(prior)
}
func (c *stateV3AssemblyChild) readGraphQLCommit(prior handle) (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyGraphQL(prior)
}
func (c *stateV3AssemblyChild) readTree(prior handle) (handle, Result) {
	if c == nil || c.session == nil {
		return handle{}, failure(DiagnosticProtocol)
	}
	return c.session.readAssemblyTree(prior)
}

// stateV3AssemblyOperation owns the only three fixed proof sessions. It has
// no assembly result API and is not an authorization boundary.
type stateV3AssemblyOperation struct {
	minor, patch, coordination *session
	quota                      *stateV3AssemblyQuota
	store                      *stateV3DocumentStore
	mu                         sync.Mutex
	next                       stateV3AssemblyRole
	running                    *stateV3AssemblyChild
	active                     bool
	ctx                        context.Context
	cancel                     context.CancelFunc
	deadline                   time.Time
}

func (op *stateV3AssemblyOperation) begin(ctx context.Context, deadline time.Time, policy gardenerrelease.StateV3Policy) Result {
	if op == nil || ctx == nil || deadline.IsZero() || !time.Now().Before(deadline) {
		return failure(DiagnosticDeadline)
	}
	if gardenerrelease.ValidateStateV3Policy(policy) != nil || !op.validSessions() {
		return failure(DiagnosticProtocol)
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.active {
		return failure(DiagnosticProtocol)
	}
	quota := &stateV3AssemblyQuota{attempts: stateV3AssemblyAttempts, bytes: stateV3AssemblyResponseBytes}
	minorLease, ok := op.minor.reserveAssembly(stateV3AssemblyMinor, stateV3AssemblyStateReads, stateV3AssemblyChildResponseBytes, quota)
	if !ok {
		return failure(DiagnosticProtocol)
	}
	patchLease, ok := op.patch.reserveAssembly(stateV3AssemblyPatch, stateV3AssemblyStateReads, stateV3AssemblyChildResponseBytes, quota)
	if !ok {
		op.minor.releaseAssembly()
		return failure(DiagnosticProtocol)
	}
	coordLease, ok := op.coordination.reserveAssembly(stateV3AssemblyCoordination, stateV3AssemblyCoordinationReads, stateV3AssemblyChildResponseBytes, quota)
	if !ok {
		op.minor.releaseAssembly()
		op.patch.releaseAssembly()
		return failure(DiagnosticProtocol)
	}
	_ = minorLease
	_ = patchLease
	_ = coordLease // Leases remain session-owned until their child begins.
	op.ctx, op.cancel = context.WithCancel(ctx)
	op.deadline, op.quota, op.store, op.next, op.active = deadline, quota, &stateV3DocumentStore{}, stateV3AssemblyMinor, true
	return Result{}
}

func (op *stateV3AssemblyOperation) beginChild(role stateV3AssemblyRole) (*stateV3AssemblyChild, Result) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if !op.active || op.running != nil || role != op.next || op.ctx.Err() != nil || !time.Now().Before(op.deadline) {
		return nil, failure(DiagnosticProtocol)
	}
	var session *session
	var next stateV3AssemblyRole
	switch role {
	case stateV3AssemblyMinor:
		session, next = op.minor, stateV3AssemblyPatch
	case stateV3AssemblyPatch:
		session, next = op.patch, stateV3AssemblyCoordination
	case stateV3AssemblyCoordination:
		session, next = op.coordination, 0
	default:
		return nil, failure(DiagnosticProtocol)
	}
	if !session.activateAssembly(op.ctx, op.deadline, role) {
		return nil, failure(DiagnosticProtocol)
	}
	child := &stateV3AssemblyChild{session: session, store: op.store}
	op.running, op.next = child, next
	return child, Result{}
}

func (op *stateV3AssemblyOperation) finishChild(child *stateV3AssemblyChild) Result {
	op.mu.Lock()
	defer op.mu.Unlock()
	if !op.active || child == nil || op.running != child {
		return failure(DiagnosticProtocol)
	}
	child.session.closeAssembly()
	op.running = nil
	return Result{}
}

func (op *stateV3AssemblyOperation) close() {
	if op == nil {
		return
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if !op.active {
		return
	}
	if op.cancel != nil {
		op.cancel()
	}
	for _, s := range []*session{op.minor, op.patch, op.coordination} {
		if s != nil {
			s.closeAssembly()
		}
	}
	op.active = false
	op.next = 0
	op.running = nil
	if op.store != nil {
		op.store.close()
	}
	op.store = nil
	op.quota = nil
	op.ctx = nil
	op.cancel = nil
}
func (op *stateV3AssemblyOperation) validSessions() bool {
	return op != nil && op.minor != nil && op.patch != nil && op.coordination != nil && op.minor != op.patch && op.minor != op.coordination && op.patch != op.coordination
}
