// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "encoding/json"

type ReconciliationCategory string

const (
	ReconcileRecorded             ReconciliationCategory = "recorded"
	ReconcileAcknowledgedNoRecord ReconciliationCategory = "acknowledged_no_record"
)

// DispatchInputs is exactly the B01 wire contract. There is deliberately no
// actor, ref, SHA, workflow, feedback-only, or force field.
type DispatchInputs struct {
	ContractVersion string `json:"contract_version"`
	Command         string `json:"command"`
	Version         string `json:"version"`
	Context         string `json:"context"`
}

type ReconciliationInspection struct {
	OK       bool                   `json:"ok"`
	Category ReconciliationCategory `json:"category"`
	Phase    string                 `json:"phase,omitempty"`
	Inputs   DispatchInputs         `json:"inputs"`
}

// InspectReconciliation emits operator-approved retry inputs from the original
// strictly decoded dispatch. operationRaw is nil only for an acknowledged
// request for which no signed state record exists; that category never creates
// an operation automatically.
func InspectReconciliation(requestRaw, operationRaw []byte) (ReconciliationInspection, error) {
	request, err := DecodeDispatchJSON(requestRaw)
	if err != nil {
		return ReconciliationInspection{}, err
	}
	contextRaw, err := json.Marshal(request.Context)
	if err != nil {
		return ReconciliationInspection{}, typedContractError("invalid_context_json")
	}
	// Context's Go field tags preserve the exact seven-key wire schema.
	inputs := DispatchInputs{ContractVersion: request.ContractVersion, Command: request.Command, Version: request.Version, Context: string(contextRaw)}
	if len(operationRaw) == 0 {
		return ReconciliationInspection{OK: true, Category: ReconcileAcknowledgedNoRecord, Inputs: inputs}, nil
	}
	operation, err := InspectOperation(operationRaw)
	if err != nil {
		return ReconciliationInspection{}, err
	}
	if operation.RequestKey != request.RequestKey || operation.RequestSHA256 != request.RequestSHA256 || operation.RepositoryID != request.Context.RepositoryID || operation.RepositoryFullName != request.Context.RepositoryFullName || operation.IssueNumber != request.Context.IssueNumber || operation.OriginalCommentID != request.Context.OriginalCommentID || operation.AcknowledgementCommentID != request.Context.AcknowledgementCommentID || operation.Command != request.Command || operation.Version != request.Version || operation.PolicyRevision != request.Context.PolicyRevision {
		return ReconciliationInspection{}, newReleaseError(ErrorClassStateConflict, "reconciliation_record_mismatch")
	}
	return ReconciliationInspection{OK: true, Category: ReconcileRecorded, Phase: operation.Phase, Inputs: inputs}, nil
}
