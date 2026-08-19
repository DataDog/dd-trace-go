// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyMutation removes from the workspace the work the agent is being asked to
// redo. It fails rather than degrades: a mutation that no longer matches the tree
// means the record is stale, and silently running the task anyway would produce a
// result that looks valid but measures nothing.
func ApplyMutation(ctx context.Context, workspace, mutationsDir string, m Mutation) error {
	switch m.Kind {
	case MutationDeletePaths:
		for _, rel := range m.Paths {
			target, err := safeJoin(workspace, rel)
			if err != nil {
				return err
			}
			if _, err := os.Stat(target); err != nil {
				if os.IsNotExist(err) {
					if m.AllowMissing {
						continue
					}
					return fmt.Errorf("mutation path %q does not exist: the record is stale for this ref", rel)
				}
				return err
			}
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("delete %q: %w", rel, err)
			}
		}
		return nil

	case MutationApplyPatch:
		patch, err := safeJoin(mutationsDir, m.Patch)
		if err != nil {
			return err
		}
		if _, err := os.Stat(patch); err != nil {
			return fmt.Errorf("patch %q: %w", m.Patch, err)
		}
		if _, err := runGit(ctx, workspace, "apply", "--check", patch); err != nil {
			return fmt.Errorf("patch %q does not apply: the record is stale for this ref: %w", m.Patch, err)
		}
		if _, err := runGit(ctx, workspace, "apply", patch); err != nil {
			return fmt.Errorf("apply patch %q: %w", m.Patch, err)
		}
		return nil

	case MutationNone:
		return checkAbsent(workspace, m.AssertAbsent)

	default:
		return fmt.Errorf("unknown mutation kind %q", m.Kind)
	}
}

// CheckMutation reports whether a mutation would apply to a tree, without
// modifying it. Being non-destructive is what lets a whole dataset be verified
// against a single materialised tree per ref.
func CheckMutation(ctx context.Context, tree, mutationsDir string, m Mutation) error {
	switch m.Kind {
	case MutationDeletePaths:
		for _, rel := range m.Paths {
			target, err := safeJoin(tree, rel)
			if err != nil {
				return err
			}
			if _, err := os.Stat(target); err != nil {
				if os.IsNotExist(err) {
					if m.AllowMissing {
						continue
					}
					return fmt.Errorf("mutation path %q does not exist", rel)
				}
				return err
			}
		}
		return nil

	case MutationApplyPatch:
		patch, err := safeJoin(mutationsDir, m.Patch)
		if err != nil {
			return err
		}
		if _, err := os.Stat(patch); err != nil {
			return fmt.Errorf("patch %q: %w", m.Patch, err)
		}
		if _, err := runGit(ctx, tree, "apply", "--check", patch); err != nil {
			return fmt.Errorf("patch %q does not apply: %w", m.Patch, err)
		}
		return nil

	case MutationNone:
		return checkAbsent(tree, m.AssertAbsent)

	default:
		return fmt.Errorf("unknown mutation kind %q", m.Kind)
	}
}

// checkAbsent is the staleness guard for MutationNone. A task that asks the
// agent to build something the repository lacks stops being that task once the
// thing exists, and with no mutation to apply there is nothing else to notice.
func checkAbsent(tree string, paths []string) error {
	for _, rel := range paths {
		target, err := safeJoin(tree, rel)
		if err != nil {
			return err
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("path %q already exists: the task no longer asks for something missing", rel)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// VerifyMutations checks every spec against one ref. Comparisons run this for both
// refs up front, so a record that has gone stale fails in seconds rather than
// hours into a run.
//
// The failure this really guards against is a mutation that applies to one ref but
// not the other: the two sides would silently stop being the same task.
func VerifyMutations(ctx context.Context, repoDir, mutationsDir, ref string, specs []*TaskSpec) error {
	scratch, err := os.MkdirTemp("", "agent-eval-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	tree := filepath.Join(scratch, "tree")
	if err := MaterializeTree(ctx, repoDir, ref, tree); err != nil {
		return err
	}
	for _, spec := range specs {
		if err := CheckMutation(ctx, tree, mutationsDir, spec.Mutation); err != nil {
			return fmt.Errorf("task %s at ref %s: %w", spec.TaskID, ref, err)
		}
	}
	return nil
}

// safeJoin resolves rel inside root and refuses to escape it. Mutation paths come
// from dataset records, which are data, so they get the same treatment as any
// other untrusted path input.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative", rel)
	}
	joined := filepath.Join(root, rel)
	within, err := filepath.Rel(root, joined)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %s", rel, root)
	}
	return joined, nil
}
