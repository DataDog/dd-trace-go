// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failSecondBranchPushRunner struct {
	runner CommandRunner
	pushes int
}

func (r *failSecondBranchPushRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if command.Path == "git" && containsArg(command.Args, "push") {
		r.pushes++
		if r.pushes == 2 {
			return CommandResult{}, errors.New("injected second-branch failure")
		}
	}
	return r.runner.Run(ctx, command)
}

type recordingReservationPersister struct {
	records []Record
}

func (p *recordingReservationPersister) PersistReservation(_ context.Context, decision ReservationDecision, _ string) (string, error) {
	record := decision.Record
	record.Events = append([]Event(nil), record.Events...)
	p.records = append(p.records, record)
	return strings.Repeat(string(rune('a'+len(p.records))), 40), nil
}

func TestPreparePersistsFirstBranchBeforeSecondBranchFailure(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	runner := &failSecondBranchPushRunner{runner: ExecRunner{}}
	persister := &recordingReservationPersister{}
	verified := fixture.verified("release:prepare", PhaseSigned)
	_, err := publishBranchesWithDurableProgress(context.Background(), runner, verified, persister, LoadResult{Found: true, RemoteHead: strings.Repeat("a", 40), Record: verified.record}, remote)
	if ErrorCode(err) != "branch_push_unconfirmed" {
		t.Fatalf("error = %q, want branch_push_unconfirmed", ErrorCode(err))
	}
	if len(persister.records) != 1 {
		t.Fatalf("persist calls = %d, want one before failure", len(persister.records))
	}
	persisted := persister.records[0]
	if persisted.Phase != PhaseSigned || !hasRefEvent(persisted.Events, EventBranchPublished, "refs/heads/release-v2.9.x", fixture.sourceSHA) || hasRefEvent(persisted.Events, EventBranchPublished, "refs/heads/dev-v2.10.x", fixture.releaseSHA) {
		t.Fatalf("unexpected durable partial record: phase=%s events=%#v", persisted.Phase, persisted.Events)
	}
	remoteLine := strings.TrimSpace(runGit(t, fixture.workDir, "ls-remote", remote, "refs/heads/release-v2.9.x"))
	if fields := strings.Fields(remoteLine); len(fields) != 2 || fields[0] != fixture.sourceSHA {
		t.Fatalf("release branch line = %q, want %s", remoteLine, fixture.sourceSHA)
	}
}
