// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

// The v3 contract is intentionally additive until the GitHub-created state
// backend is implemented. Production code continues to read only schema v2.
const (
	StateV3SchemaVersion           = "3"
	StateV3PolicySchemaVersion     = "3"
	MaxStateV3DocumentBytes        = 1024 * 1024
	MaxStateV3PreparedBundleBytes  = 1_048_576
	MaxStateV3DecodedAdditionBytes = 1_049_185
	// MaxStateV3GraphQLRequestBytes is reserved for the next strict wire-model writer; this foundation makes no serialized-request claim.
	MaxStateV3GraphQLRequestBytes = 1_399_719
	StateV3PreparedAdditionCount  = 3
	MaxStateV3ReleaseFileChanges  = 81
	MaxStateV3HistoryCommits      = 1024
	StateV3PrepareHistoryOverhead = 21
	StateV3OtherHistoryOverhead   = 15
	MaxStateV3Tags                = MaxStateV3ReleaseFileChanges
	StateV3MinorStateRef          = "refs/heads/gardener-release-state/minor"
	StateV3PatchStateRef          = "refs/heads/gardener-release-state/patch"
	// StateV3CoordinationRef serializes only same-release-line claims. It is
	// not a global release lock: claims for distinct X.Y lines coexist.
	StateV3CoordinationRef                = "refs/heads/gardener-release-coordination"
	StateV3WorkflowPath                   = ".github/workflows/gardener-release.yml"
	stateV3ReservationFile                = "reservation.json"
	stateV3PreparedFile                   = "prepared.json"
	stateV3GenerationBundleFile           = "generation.bundle"
	StateV3ActiveLeasePath                = "active-operation.json"
	stateV3CoordinationArmPath            = "mutation-arm.json"
	StateV3RequiredSignatureState         = "VALID"
	StateV3RequiredRESTVerificationReason = "valid"
)

// StateV3Phase is the approved platform-signing phase progression.
type StateV3Phase string

const (
	StateV3PhaseReserved          StateV3Phase = "reserved"
	StateV3PhasePrepared          StateV3Phase = "prepared"
	StateV3PhaseBranchesPublished StateV3Phase = "branches_published"
	StateV3PhaseTestsPassed       StateV3Phase = "tests_passed"
	StateV3PhaseTagsPublished     StateV3Phase = "tags_published"
	StateV3PhaseComplete          StateV3Phase = "complete"
)

var stateV3PhaseOrder = map[StateV3Phase]int{
	StateV3PhaseReserved:          0,
	StateV3PhasePrepared:          1,
	StateV3PhaseBranchesPublished: 2,
	StateV3PhaseTestsPassed:       3,
	StateV3PhaseTagsPublished:     4,
	StateV3PhaseComplete:          5,
}

// StateV3Policy freezes the future production trust root without supplying
// production values. A reviewed policy must populate every field explicitly.
type StateV3Policy struct {
	SchemaVersion      string                    `json:"schema_version"`
	PolicyRevision     string                    `json:"policy_revision"`
	RepositoryID       string                    `json:"repository_id"`
	RepositoryFullName string                    `json:"repository_full_name"`
	StateLanes         StateV3LanePolicies       `json:"state_lanes"`
	Coordination       StateV3CoordinationPolicy `json:"coordination"`
	TaggerArtifact     StateV3TrustedArtifact    `json:"tagger_artifact"`
	App                StateV3AppIdentity        `json:"app"`
	CommitRoles        StateV3CommitRoles        `json:"commit_roles"`
	Tagger             StateV3RawIdentity        `json:"tagger"`
	Rulesets           StateV3RulesetPolicies    `json:"rulesets"`
	Test               StateV3TestPolicy         `json:"test"`
	Image              StateV3ImagePolicy        `json:"image"`
	Feedback           StateV3FeedbackPolicy     `json:"feedback"`
	Limits             StateV3CapacityLimits     `json:"limits"`
}

type StateV3LanePolicy struct {
	StateRef          string `json:"state_ref"`
	CheckpointOID     string `json:"checkpoint_oid"`
	MaxHistoryCommits int    `json:"max_history_commits"`
}

type StateV3LanePolicies struct {
	Minor StateV3LanePolicy `json:"minor"`
	Patch StateV3LanePolicy `json:"patch"`
}

// StateV3CoordinationPolicy protects the single CAS domain that owns
// per-release-line claims across the otherwise independent state lanes.
type StateV3CoordinationPolicy struct {
	StateRef          string `json:"state_ref"`
	CheckpointOID     string `json:"checkpoint_oid"`
	MaxHistoryCommits int    `json:"max_history_commits"`
}

// StateV3TrustedArtifact identifies the reviewed tagger artifact. Plan bytes
// remain collector evidence; this identity prevents a caller from choosing a
// different executable when collecting historical manifests.
type StateV3TrustedArtifact struct {
	Path    string                   `json:"path"`
	Raw     []byte                   `json:"raw"`
	SHA256  string                   `json:"sha256"`
	BlobOID string                   `json:"blob_oid"`
	TreeOID string                   `json:"tree_oid"`
	Tree    StateV3StateTreeEvidence `json:"tree"`
}

type StateV3AppIdentity struct {
	AppID          string `json:"app_id"`
	InstallationID string `json:"installation_id"`
	Slug           string `json:"slug"`
	BotLogin       string `json:"bot_login"`
	BotDatabaseID  string `json:"bot_database_id"`
}

type StateV3RawIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type StateV3AssociatedIdentity struct {
	Login      string `json:"login"`
	DatabaseID string `json:"database_id"`
	Type       string `json:"type"`
}

type StateV3OptionalAssociatedIdentity struct {
	Present  bool                      `json:"present"`
	Identity StateV3AssociatedIdentity `json:"identity"`
}

type StateV3CommitRoles struct {
	AuthorRaw              StateV3RawIdentity                `json:"author_raw"`
	AuthorREST             StateV3AssociatedIdentity         `json:"author_rest"`
	AuthorGraphQL          StateV3AssociatedIdentity         `json:"author_graphql"`
	CommitterRaw           StateV3RawIdentity                `json:"committer_raw"`
	CommitterREST          StateV3OptionalAssociatedIdentity `json:"committer_rest"`
	CommitterGraphQL       StateV3OptionalAssociatedIdentity `json:"committer_graphql"`
	SignatureSignerGraphQL StateV3AssociatedIdentity         `json:"signature_signer_graphql"`
}

type StateV3RulesetRule struct {
	Type             string `json:"type"`
	ParametersSHA256 string `json:"parameters_sha256,omitempty"`
}

// StateV3RulesetPolicy binds the exact reviewed GitHub condition syntax while
// Namespace records the derived release semantic it was reviewed to cover.
// The schema deliberately does not manufacture unapproved production globs.
type StateV3RulesetPolicy struct {
	ID              string                         `json:"id"`
	Revision        string                         `json:"revision"`
	Namespace       string                         `json:"namespace"`
	IncludePatterns []string                       `json:"include_patterns"`
	ExcludePatterns []string                       `json:"exclude_patterns"`
	Enforcement     string                         `json:"enforcement"`
	Rules           []StateV3RulesetRule           `json:"rules"`
	Attestation     StateV3RulesetAdminAttestation `json:"attestation"`
}

type StateV3RulesetAdminAttestation struct {
	SchemaVersion               string                      `json:"schema_version"`
	RulesetID                   string                      `json:"ruleset_id"`
	RulesetRevision             string                      `json:"ruleset_revision"`
	BypassVisibility            string                      `json:"bypass_visibility"`
	AdministratorBypassDisabled bool                        `json:"administrator_bypass_disabled"`
	BypassActors                []StateV3RulesetBypassActor `json:"bypass_actors"`
	Reviewer                    StateV3AssociatedIdentity   `json:"reviewer"`
	ObservedAt                  string                      `json:"observed_at"`
	ConditionsSHA256            string                      `json:"conditions_sha256"`
	RulesSHA256                 string                      `json:"rules_sha256"`
	SemanticNamespaceValidated  bool                        `json:"semantic_namespace_validated"`
}

type StateV3RulesetBypassActor struct {
	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`
	Mode      string `json:"mode"`
}

type StateV3RulesetPolicies struct {
	StateHistoryProtection          StateV3RulesetPolicy `json:"state_history_protection"`
	StateUpdateAuthorization        StateV3RulesetPolicy `json:"state_update_authorization"`
	CoordinationHistoryProtection   StateV3RulesetPolicy `json:"coordination_history_protection"`
	CoordinationUpdateAuthorization StateV3RulesetPolicy `json:"coordination_update_authorization"`
	BranchCreationAuthorization     StateV3RulesetPolicy `json:"branch_creation_authorization"`
	BranchUpdateAuthorization       StateV3RulesetPolicy `json:"branch_update_authorization"`
	TagCreationAuthorization        StateV3RulesetPolicy `json:"tag_creation_authorization"`
	TagImmutability                 StateV3RulesetPolicy `json:"tag_immutability"`
}

type StateV3TestPolicy struct {
	WorkflowID      string   `json:"workflow_id"`
	WorkflowPath    string   `json:"workflow_path"`
	WorkflowSHA256  string   `json:"workflow_sha256"`
	Event           string   `json:"event"`
	RequiredJobs    []string `json:"required_jobs"`
	DeadlineSeconds int      `json:"deadline_seconds"`
}

type StateV3ImagePolicy struct {
	WorkflowID          string `json:"workflow_id"`
	WorkflowPath        string `json:"workflow_path"`
	WorkflowSHA256      string `json:"workflow_sha256"`
	ChildWorkflowSHA256 string `json:"child_workflow_sha256"`
}

type StateV3FeedbackPolicy struct {
	GardenerAuthorID    string `json:"gardener_author_id"`
	GardenerAuthorLogin string `json:"gardener_author_login"`
}

type StateV3CapacityLimits struct {
	PreparedBundleBytes  int64 `json:"prepared_bundle_bytes"`
	DecodedAdditionBytes int64 `json:"decoded_addition_bytes"`
	ReleaseFileChanges   int   `json:"release_file_changes"`
}

// StateV3Record contains only persisted release semantics. Authentication of
// the containing state commit is supplied separately as StateV3Authentication
// to avoid a circular commit-OID claim inside the commit itself.
type StateV3Record struct {
	SchemaVersion     string                     `json:"schema_version"`
	Reservation       StateV3Reservation         `json:"reservation"`
	Binding           StateV3ExecutionBinding    `json:"binding"`
	Phase             StateV3Phase               `json:"phase"`
	Prepared          *StateV3PreparedState      `json:"prepared,omitempty"`
	Commit            *StateV3AdoptedCommit      `json:"commit,omitempty"`
	MutationArms      []StateV3MutationArm       `json:"mutation_arms,omitempty"`
	BranchEvidence    []StateV3BranchEvidence    `json:"branch_evidence,omitempty"`
	TagPlans          []StateV3TagPlan           `json:"tag_plans,omitempty"`
	TagIntents        []StateV3TagIntent         `json:"tag_intents,omitempty"`
	TagObjectEvidence []StateV3TagObjectEvidence `json:"tag_object_evidence,omitempty"`
	TagEvidence       []StateV3TagEvidence       `json:"tag_evidence,omitempty"`
	TestEvidence      *StateV3TestEvidence       `json:"test_evidence,omitempty"`
	PreparePRIntent   *StateV3PreparePRIntent    `json:"prepare_pr_intent,omitempty"`
	PreparePREvidence *StateV3PreparePREvidence  `json:"prepare_pr_evidence,omitempty"`
	OutcomeEvidence   *StateV3OutcomeEvidence    `json:"outcome_evidence,omitempty"`
	Events            []StateV3Event             `json:"events"`
}

type StateV3Reservation struct {
	ContractVersion          string                           `json:"contract_version"`
	RequestKey               string                           `json:"request_key"`
	RequestSHA256            string                           `json:"request_sha256"`
	RepositoryID             string                           `json:"repository_id"`
	RepositoryFullName       string                           `json:"repository_full_name"`
	IssueNumber              string                           `json:"issue_number"`
	OriginalCommentID        string                           `json:"original_comment_id"`
	AcknowledgementCommentID string                           `json:"acknowledgement_comment_id"`
	BodySnapshot             string                           `json:"body_snapshot"`
	Marker                   string                           `json:"marker"`
	ValidatedActorID         string                           `json:"validated_actor_id"`
	ValidatedActorLogin      string                           `json:"validated_actor_login"`
	Command                  string                           `json:"command"`
	RequestedVersion         string                           `json:"requested_version"`
	ResolvedVersion          string                           `json:"resolved_version"`
	DevelopmentVersion       string                           `json:"development_version,omitempty"`
	ReleaseLine              string                           `json:"release_line"`
	GenerationVersion        string                           `json:"generation_version"`
	SourceRef                string                           `json:"source_ref"`
	SourceOID                string                           `json:"source_oid"`
	PolicyRevision           string                           `json:"policy_revision"`
	ReleaseBranch            string                           `json:"release_branch"`
	DevelopmentBranch        string                           `json:"development_branch,omitempty"`
	VersionResolution        StateV3VersionResolutionEvidence `json:"version_resolution"`
	CoordinationClaim        StateV3ReleaseLineClaimEvidence  `json:"coordination_claim"`
}

type StateV3RefObservation struct {
	Ref string `json:"ref"`
	OID string `json:"oid"`
}

// StateV3SourceVersionEvidence binds the parsed source version to an exact,
// independently reread source commit, tree, and version-file blob.
type StateV3SourceVersionEvidence struct {
	Ref     string                     `json:"ref"`
	OID     string                     `json:"oid"`
	Path    string                     `json:"path"`
	Raw     []byte                     `json:"raw"`
	SHA256  string                     `json:"sha256"`
	BlobOID string                     `json:"blob_oid"`
	Version string                     `json:"version"`
	Commit  StateV3StateCommitEvidence `json:"commit"`
	Tree    StateV3StateTreeEvidence   `json:"tree"`
}

// StateV3IncompleteTagDerivation binds historical tag completeness to the
// exact tagger plan generated from the peeled root-tag source commit.
type StateV3IncompleteTagDerivation struct {
	Version          string                          `json:"version"`
	ManifestOID      string                          `json:"manifest_oid"`
	PeeledCommitOID  string                          `json:"peeled_commit_oid"`
	SourceTreeOID    string                          `json:"source_tree_oid"`
	RootTag          StateV3HistoricalTagEvidence    `json:"root_tag"`
	HistoricalCommit StateV3StateCommitEvidence      `json:"historical_commit"`
	HistoricalTree   StateV3StateTreeEvidence        `json:"historical_tree"`
	ToolPath         string                          `json:"tool_path"`
	ToolSHA256       string                          `json:"tool_sha256"`
	ToolArtifact     StateV3TrustedArtifact          `json:"tool_artifact"`
	Execution        StateV3PlanExecutionAttestation `json:"execution"`
	PlanRaw          []byte                          `json:"plan_raw"`
	PlanSHA256       string                          `json:"plan_sha256"`
	PlanBlobOID      string                          `json:"plan_blob_oid"`
	ExpectedTagRefs  []string                        `json:"expected_tag_refs"`
	PresentTagRefs   []string                        `json:"present_tag_refs"`
}

// StateV3PlanExecutionAttestation is a closed collector obligation: the
// strict backend must execute the approved artifact against this exact tree
// and attest to the exact plan blob. The semantic schema deliberately does
// not pretend that self-hashed plan bytes prove process execution.
type StateV3PlanExecutionAttestation struct {
	SchemaVersion   string                 `json:"schema_version"`
	Collector       string                 `json:"collector"`
	ToolArtifact    StateV3TrustedArtifact `json:"tool_artifact"`
	SourceCommitOID string                 `json:"source_commit_oid"`
	SourceTreeOID   string                 `json:"source_tree_oid"`
	PlanSHA256      string                 `json:"plan_sha256"`
	PlanBlobOID     string                 `json:"plan_blob_oid"`
	Verified        bool                   `json:"verified"`
}

// StateV3HistoricalTagEvidence is the independently reread annotated root tag
// and its peel. The backend must collect its raw REST/GraphQL/Git evidence.
type StateV3HistoricalTagEvidence struct {
	TagObjectOID    string `json:"tag_object_oid"`
	ObjectType      string `json:"object_type"`
	TargetType      string `json:"target_type"`
	PeeledCommitOID string `json:"peeled_commit_oid"`
	Signature       string `json:"signature"`
}

type StateV3RemoteRefsEvidence struct {
	Complete              bool                             `json:"complete"`
	Branches              []StateV3RefObservation          `json:"branches"`
	Tags                  []StateV3RefObservation          `json:"tags"`
	IncompleteTagVersions []string                         `json:"incomplete_tag_versions"`
	IncompleteDerivations []StateV3IncompleteTagDerivation `json:"incomplete_derivations"`
}

type StateV3VersionResolutionEvidence struct {
	RequestKey       string                       `json:"request_key"`
	RequestSHA256    string                       `json:"request_sha256"`
	Command          string                       `json:"command"`
	RequestedVersion string                       `json:"requested_version"`
	ReleaseLine      string                       `json:"release_line"`
	Source           StateV3SourceVersionEvidence `json:"source"`
	RemoteRefs       StateV3RemoteRefsEvidence    `json:"remote_refs"`
	// Coordination is the only authorizing cross-lane conflict authority.
	// LaneHeads are intentionally absent: two lane ref reads cannot provide an
	// atomic same-line exclusion decision.
	Coordination StateV3CoordinationObservation `json:"coordination"`
	// LaneHeads is retained only for backward-compatible fixture decoding. It
	// is non-authorizing and must never participate in conflict discovery.
	LaneHeads          []StateV3LaneHeadObservation `json:"lane_heads,omitempty"`
	ExistingOperations []ExistingOperation          `json:"existing_operations"`
	Result             VersionResolution            `json:"result"`
}

// StateV3CoordinationObservation captures the independently reread CAS
// parent and complete coordination tree immediately before line-claim
// acquisition. The future API backend must reconcile these observations before
// issuing the one-shot mutation.
// StateV3LaneHeadObservation is non-authorizing compatibility evidence. The
// coordination claim is the sole authority for cross-lane conflict absence.
type StateV3LaneHeadObservation struct {
	StateRef      string               `json:"state_ref"`
	HeadOID       string               `json:"head_oid"`
	CheckpointOID string               `json:"checkpoint_oid"`
	Snapshot      StateV3StateSnapshot `json:"snapshot"`
}

type StateV3CoordinationObservation struct {
	StateRef      string                            `json:"state_ref"`
	CheckpointOID string                            `json:"checkpoint_oid"`
	Head          StateV3StateCommitEvidence        `json:"head"`
	Tree          StateV3StateTreeEvidence          `json:"tree"`
	Claims        []StateV3ReleaseLineClaimEvidence `json:"claims"`
	// Authentication is transient backend evidence, never part of the raw
	// release record. It remains fixture-compatible while callers pass the
	// same authentication through StateV3Authentication.Coordination.
	Authentication StateV3CoordinationAuthentication `json:"-"`
}

// StateV3CoordinationAuthentication authenticates the shared release-line
// claim domain from its reviewed checkpoint to the exact pre-acquisition head.
// Unlike lane state, this tree contains only compact derived line claims.
type StateV3CoordinationAuthentication struct {
	StateRef      string                        `json:"state_ref"`
	HeadOID       string                        `json:"head_oid"`
	CheckpointOID string                        `json:"checkpoint_oid"`
	Current       StateV3CoordinationSnapshot   `json:"current"`
	Predecessors  []StateV3CoordinationSnapshot `json:"predecessors"`
	// LaneTerminations is transient backend evidence. It is deliberately not
	// persisted in a release record, preventing recursive state histories.
	LaneTerminations []StateV3Authentication `json:"-"`
}

type StateV3CoordinationSnapshot struct {
	Commit  StateV3StateCommitEvidence          `json:"commit"`
	Tree    StateV3StateTreeEvidence            `json:"tree"`
	Claims  []StateV3ReleaseLineClaimEvidence   `json:"claims"`
	Arm     *StateV3CoordinationArmEvidence     `json:"arm,omitempty"`
	Release *StateV3CoordinationReleaseEvidence `json:"release,omitempty"`
}

// StateV3CoordinationMutationArm is persisted before a one-shot coordination
// CAS. A pending arm blocks mutation retry until exact read-only adoption.
type StateV3CoordinationMutationArm struct {
	Operation            string `json:"operation"`
	Ref                  string `json:"ref"`
	ClaimPath            string `json:"claim_path"`
	RequestKey           string `json:"request_key"`
	ReleaseLine          string `json:"release_line"`
	LaneRef              string `json:"lane_ref"`
	ResolvedVersion      string `json:"resolved_version"`
	ExpectedHeadOID      string `json:"expected_head_oid"`
	ExpectedClaimBlobOID string `json:"expected_claim_blob_oid,omitempty"`
	// IntendedClaimSHA256 is required for claim acquisition and binds the
	// canonical immutable claim document before its one-shot CAS.
	IntendedClaimSHA256 string `json:"intended_claim_sha256,omitempty"`
	Attempt             int    `json:"attempt"`
}

type StateV3CoordinationArmEvidence struct {
	Path    string                         `json:"path"`
	Raw     []byte                         `json:"raw"`
	SHA256  string                         `json:"sha256"`
	BlobOID string                         `json:"blob_oid"`
	Arm     StateV3CoordinationMutationArm `json:"arm"`
}

// StateV3CoordinationReleaseEvidence is an ordinary, one-shot claim deletion.
// Administrative resolution is deliberately not represented by this schema.
type StateV3CoordinationReleaseEvidence struct {
	Path            string                         `json:"path"`
	ClaimSHA256     string                         `json:"claim_sha256"`
	ClaimBlobOID    string                         `json:"claim_blob_oid"`
	ClaimCommitOID  string                         `json:"claim_commit_oid"`
	Response        StateV3MutationResponse        `json:"response"`
	ObservedRefOID  string                         `json:"observed_ref_oid"`
	LaneTermination StateV3LaneTerminationEvidence `json:"lane_termination"`
}

// StateV3LaneTerminationEvidence binds ordinary claim release to a precise
// completed-record then lease-release transition on the owning lane.
type StateV3LaneTerminationEvidence struct {
	StateRef                 string `json:"state_ref"`
	CheckpointOID            string `json:"checkpoint_oid"`
	CompleteRecordOID        string `json:"complete_record_oid"`
	CompleteRecordSHA256     string `json:"complete_record_sha256"`
	CompleteRecordBlobOID    string `json:"complete_record_blob_oid"`
	LeaseReleaseHeadOID      string `json:"lease_release_head_oid"`
	ObservedRefOID           string `json:"observed_ref_oid"`
	CoordinationClaimOID     string `json:"coordination_claim_oid"`
	CoordinationClaimBlobOID string `json:"coordination_claim_blob_oid"`
	CoordinationClaimSHA256  string `json:"coordination_claim_sha256"`
}

// StateV3ReleaseLineClaimEvidence is the durable, CAS-created authority for
// a release line. It remains active until its lane record is complete and the
// lane lease has been released. ClaimPath is derived from ReleaseLine.
type StateV3ReleaseLineClaimEvidence struct {
	Path           string                     `json:"path"`
	Raw            []byte                     `json:"raw"`
	SHA256         string                     `json:"sha256"`
	BlobOID        string                     `json:"blob_oid"`
	Claim          StateV3ReleaseLineClaim    `json:"claim"`
	Acquired       StateV3MutationResponse    `json:"acquired"`
	Commit         StateV3StateCommitEvidence `json:"commit"`
	Tree           StateV3StateTreeEvidence   `json:"tree"`
	ObservedRefOID string                     `json:"observed_ref_oid"`
}

type StateV3ReleaseLineClaim struct {
	ReleaseLine             string `json:"release_line"`
	RequestKey              string `json:"request_key"`
	RequestSHA256           string `json:"request_sha256"`
	LaneRef                 string `json:"lane_ref"`
	LaneExpectedHeadOID     string `json:"lane_expected_head_oid"`
	ReservationSHA256       string `json:"reservation_sha256"`
	VersionResolutionSHA256 string `json:"version_resolution_sha256"`
	Command                 string `json:"command"`
	ResolvedVersion         string `json:"resolved_version"`
	DevelopmentVersion      string `json:"development_version,omitempty"`
	// State is the closed coordination lifecycle state. Claims are immutable
	// while active; ordinary release is represented only by a typed tree
	// deletion transition, never by mutating this document.
	State   string       `json:"state"`
	Phase   StateV3Phase `json:"phase"`
	Attempt int          `json:"attempt"`
}

type StateV3ExecutionBinding struct {
	RequestKey         string `json:"request_key"`
	RequestSHA256      string `json:"request_sha256"`
	WorkflowSHA        string `json:"workflow_sha"`
	WorkflowRef        string `json:"workflow_ref"`
	WorkflowPath       string `json:"workflow_path"`
	WorkflowFileSHA256 string `json:"workflow_file_sha256"`
	WorkflowRunID      string `json:"workflow_run_id"`
	WorkflowRunAttempt int    `json:"workflow_run_attempt"`
	PolicyRevision     string `json:"policy_revision"`
}

type StateV3PreparedState struct {
	Bundle                    StateV3BundleIdentity       `json:"bundle"`
	StateFiles                []StateV3PreparedFile       `json:"state_files"`
	AdditionCount             int                         `json:"addition_count"`
	TotalDecodedAdditionBytes int64                       `json:"total_decoded_addition_bytes"`
	ToolDigest                string                      `json:"tool_digest"`
	ValidatorDigest           string                      `json:"validator_digest"`
	Mutation                  StateV3CommitMutationIntent `json:"mutation"`
}

type StateV3BundleIdentity struct {
	Path            string `json:"path"`
	SHA256          string `json:"sha256"`
	BlobOID         string `json:"blob_oid"`
	SizeBytes       int64  `json:"size_bytes"`
	PrerequisiteOID string `json:"prerequisite_oid"`
}

type StateV3PreparedFile struct {
	Path      string `json:"path"`
	BlobOID   string `json:"blob_oid"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// StateV3StagedEnvelopeEvidence authenticates the physical three-addition
// commit that precedes the one-file state transition into prepared.
type StateV3StagedEnvelopeEvidence struct {
	Prepared StateV3PreparedState `json:"prepared"`
	TagPlans []StateV3TagPlan     `json:"tag_plans"`
	Files    []StateV3StagedFile  `json:"files"`
}

type StateV3StagedFile struct {
	Path      string `json:"path"`
	Raw       []byte `json:"raw"`
	SHA256    string `json:"sha256"`
	BlobOID   string `json:"blob_oid"`
	SizeBytes int64  `json:"size_bytes"`
}

type stateV3PreparedManifest struct {
	SchemaVersion             string                      `json:"schema_version"`
	Bundle                    StateV3BundleIdentity       `json:"bundle"`
	AdditionCount             int                         `json:"addition_count"`
	TotalDecodedAdditionBytes int64                       `json:"total_decoded_addition_bytes"`
	ToolDigest                string                      `json:"tool_digest"`
	ValidatorDigest           string                      `json:"validator_digest"`
	Mutation                  StateV3CommitMutationIntent `json:"mutation"`
	TagPlans                  []StateV3TagPlan            `json:"tag_plans"`
}

type StateV3CommitMutationIntent struct {
	RepositoryID       string                        `json:"repository_id"`
	RepositoryFullName string                        `json:"repository_full_name"`
	TargetRef          string                        `json:"target_ref"`
	ExpectedHeadOID    string                        `json:"expected_head_oid"`
	ExpectedTreeOID    string                        `json:"expected_tree_oid"`
	Message            string                        `json:"message"`
	FileChangesSHA256  string                        `json:"file_changes_sha256"`
	FileChanges        []StateV3FileChange           `json:"file_changes"`
	Branches           []StateV3BranchMutationIntent `json:"branches"`
}

type StateV3BranchMutationIntent struct {
	Ref            string `json:"ref"`
	ExpectedOldOID string `json:"expected_old_oid"`
	Target         string `json:"target"`
}

// StateV3MutationArm is committed before a release-repository mutation. An
// unmatched final arm is a valid terminal mutation state: only read-only
// reconciliation may append its immediately matching result.
type StateV3MutationArm struct {
	OperationKind       string `json:"operation_kind"`
	Ref                 string `json:"ref"`
	ExpectedOldMissing  bool   `json:"expected_old_missing"`
	ExpectedOldOID      string `json:"expected_old_oid,omitempty"`
	IntendedObjectKnown bool   `json:"intended_object_known"`
	IntendedObjectOID   string `json:"intended_object_oid,omitempty"`
	IntentSHA256        string `json:"intent_sha256"`
	Attempt             int    `json:"attempt"`
}

type StateV3BranchEvidence struct {
	Intent         StateV3BranchMutationIntent `json:"intent"`
	Response       StateV3MutationResponse     `json:"response"`
	ObjectOID      string                      `json:"object_oid"`
	ObservedRefOID string                      `json:"observed_ref_oid"`
	Status         string                      `json:"status"`
	Disposition    string                      `json:"disposition"`
}

type StateV3FileChange struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Mode      string `json:"mode,omitempty"`
	BlobOID   string `json:"blob_oid,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// StateV3AdoptedCommit is independent mutation-response, ref, REST, GraphQL,
// and platform-signature evidence for the platform-generated commit OID.
type StateV3MutationResponse struct {
	Observation string `json:"observation"`
	Attempts    int    `json:"attempts"`
	OID         string `json:"oid,omitempty"`
}

type StateV3AdoptedCommit struct {
	OID                   string                  `json:"oid"`
	MutationResponse      StateV3MutationResponse `json:"mutation_response"`
	ObservedRefOID        string                  `json:"observed_ref_oid"`
	RESTOID               string                  `json:"rest_oid"`
	GraphQLOID            string                  `json:"graphql_oid"`
	Ref                   string                  `json:"ref"`
	ParentOID             string                  `json:"parent_oid"`
	TreeOID               string                  `json:"tree_oid"`
	Message               string                  `json:"message"`
	FileChangesSHA256     string                  `json:"file_changes_sha256"`
	FileChanges           []StateV3FileChange     `json:"file_changes"`
	RESTVerified          bool                    `json:"rest_verified"`
	RESTReason            string                  `json:"rest_reason"`
	GraphQLSignatureValid bool                    `json:"graphql_signature_valid"`
	WasSignedByGitHub     bool                    `json:"was_signed_by_github"`
	SignatureState        string                  `json:"signature_state"`
	Roles                 StateV3CommitRoles      `json:"roles"`
}

// StateV3TagPlan contains every retry-stable annotated-tag input known before
// the platform-generated release commit OID exists.
type StateV3TagPlan struct {
	Name       string             `json:"name"`
	Ref        string             `json:"ref"`
	Message    string             `json:"message"`
	TargetRef  string             `json:"target_ref"`
	Tagger     StateV3RawIdentity `json:"tagger"`
	TaggerDate string             `json:"tagger_date"`
	Signature  string             `json:"signature"`
}

// StateV3TagIntent is finalized after commit adoption and before tests. Its
// expected object OID is deterministic from the plan and adopted target OID.
type StateV3TagIntent struct {
	Name                 string             `json:"name"`
	Ref                  string             `json:"ref"`
	Message              string             `json:"message"`
	ObjectType           string             `json:"object_type"`
	TargetType           string             `json:"target_type"`
	TargetOID            string             `json:"target_oid"`
	ExpectedTagObjectOID string             `json:"expected_tag_object_oid"`
	Tagger               StateV3RawIdentity `json:"tagger"`
	TaggerDate           string             `json:"tagger_date"`
	Signature            string             `json:"signature"`
}

// StateV3TagEvidence is populated only after an independently reread tag
// object exactly matches its already-persisted intent and the adopted commit.
type StateV3TagObjectEvidence struct {
	Intent           StateV3TagIntent        `json:"intent"`
	Response         StateV3MutationResponse `json:"response"`
	RESTTagObjectOID string                  `json:"rest_tag_object_oid"`
	PeeledCommitOID  string                  `json:"peeled_commit_oid"`
}

type StateV3TagEvidence struct {
	Name             string                  `json:"name"`
	Ref              string                  `json:"ref"`
	TagObjectOID     string                  `json:"tag_object_oid"`
	RefResponse      StateV3MutationResponse `json:"ref_response"`
	ObservedRefOID   string                  `json:"observed_ref_oid"`
	RESTTagObjectOID string                  `json:"rest_tag_object_oid"`
	Message          string                  `json:"message"`
	ObjectType       string                  `json:"object_type"`
	TargetType       string                  `json:"target_type"`
	TargetOID        string                  `json:"target_oid"`
	PeeledCommitOID  string                  `json:"peeled_commit_oid"`
	Signature        string                  `json:"signature"`
	Tagger           StateV3RawIdentity      `json:"tagger"`
	TaggerDate       string                  `json:"tagger_date"`
}

type StateV3TestJobEvidence struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Attempt    int    `json:"attempt"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type StateV3TestRunEvidence struct {
	TargetBranch string                   `json:"target_branch"`
	TargetOID    string                   `json:"target_oid"`
	RunID        string                   `json:"run_id"`
	Attempt      int                      `json:"attempt"`
	Status       string                   `json:"status"`
	Conclusion   string                   `json:"conclusion"`
	Jobs         []StateV3TestJobEvidence `json:"jobs"`
}

type StateV3TestEvidence struct {
	SchemaVersion  string                   `json:"schema_version"`
	Repository     string                   `json:"repository"`
	WorkflowPath   string                   `json:"workflow_path"`
	WorkflowID     string                   `json:"workflow_id"`
	WorkflowSHA256 string                   `json:"workflow_sha256"`
	Event          string                   `json:"event"`
	ReleaseOID     string                   `json:"release_oid"`
	RequiredJobs   []string                 `json:"required_jobs"`
	Runs           []StateV3TestRunEvidence `json:"runs"`
}

type StateV3PreparePRIntent struct {
	Repository     string `json:"repository"`
	Base           string `json:"base"`
	Head           string `json:"head"`
	HeadOID        string `json:"head_oid"`
	Title          string `json:"title"`
	BodyMarker     string `json:"body_marker"`
	ExpectedAbsent bool   `json:"expected_absent"`
}

type StateV3PreparePREvidence struct {
	SchemaVersion  string                  `json:"schema_version"`
	Intent         StateV3PreparePRIntent  `json:"intent"`
	Response       StateV3MutationResponse `json:"response"`
	Number         string                  `json:"number"`
	URL            string                  `json:"url"`
	ObservedHead   string                  `json:"observed_head"`
	ObservedOID    string                  `json:"observed_oid"`
	ObservedBase   string                  `json:"observed_base"`
	ObservedMarker string                  `json:"observed_marker"`
	Status         string                  `json:"status"`
	Disposition    string                  `json:"disposition"`
}

type StateV3ImageEvidence struct {
	WorkflowID string                 `json:"workflow_id"`
	Detail     ImagePromotionEvidence `json:"detail"`
}

type StateV3OutcomeEvidence struct {
	SchemaVersion    string                 `json:"schema_version"`
	RequestKey       string                 `json:"request_key"`
	Command          string                 `json:"command"`
	ReleaseOID       string                 `json:"release_oid"`
	PriorStateSHA256 string                 `json:"prior_state_sha256"`
	Publication      WorkOutcome            `json:"publication"`
	PreparePR        WorkOutcome            `json:"prepare_pr"`
	Images           WorkOutcome            `json:"images"`
	ImageEvidence    []StateV3ImageEvidence `json:"image_evidence"`
}

type StateV3EventKind string

const (
	StateV3EventReserved              StateV3EventKind = "reserved"
	StateV3EventMutationArmed         StateV3EventKind = "mutation_armed"
	StateV3EventPhaseAdvanced         StateV3EventKind = "phase_advanced"
	StateV3EventPlatformCommitAdopted StateV3EventKind = "platform_commit_adopted"
	StateV3EventBranchPublished       StateV3EventKind = "branch_published"
	StateV3EventTestsPassed           StateV3EventKind = "tests_passed"
	StateV3EventTagObjectCreated      StateV3EventKind = "tag_object_created"
	StateV3EventTagPublished          StateV3EventKind = "tag_published"
	StateV3EventPreparePRRecorded     StateV3EventKind = "prepare_pr_recorded"
	StateV3EventOutcomeRecorded       StateV3EventKind = "outcome_recorded"
)

type StateV3Event struct {
	Sequence       int              `json:"sequence"`
	PreviousDigest string           `json:"previous_digest"`
	Kind           StateV3EventKind `json:"kind"`
	Phase          StateV3Phase     `json:"phase"`
	Ref            string           `json:"ref,omitempty"`
	ExpectedOldOID string           `json:"expected_old_oid,omitempty"`
	ObjectOID      string           `json:"object_oid,omitempty"`
	Disposition    string           `json:"disposition,omitempty"`
	EvidenceSHA256 string           `json:"evidence_sha256"`
	Digest         string           `json:"digest"`
}

// StateV3Authentication is independently collected evidence for the commit
// containing a decoded record and its bounded ancestry to a reviewed checkpoint.
type StateV3Authentication struct {
	StateRef      string                 `json:"state_ref"`
	HeadOID       string                 `json:"head_oid"`
	CheckpointOID string                 `json:"checkpoint_oid"`
	Current       StateV3StateSnapshot   `json:"current"`
	Predecessors  []StateV3StateSnapshot `json:"predecessors"`
	// Coordination is transient independently-read evidence for the exact
	// coordination observation persisted by the record.
	Coordination *StateV3CoordinationAuthentication `json:"-"`
}

type StateV3ActiveOperationLease struct {
	SchemaVersion            string `json:"schema_version"`
	RepositoryID             string `json:"repository_id"`
	RepositoryFullName       string `json:"repository_full_name"`
	OriginalCommentID        string `json:"original_comment_id"`
	Command                  string `json:"command"`
	RequestedVersion         string `json:"requested_version"`
	ResolvedVersion          string `json:"resolved_version"`
	DevelopmentVersion       string `json:"development_version,omitempty"`
	SourceRef                string `json:"source_ref"`
	SourceOID                string `json:"source_oid"`
	RequestKey               string `json:"request_key"`
	RequestSHA256            string `json:"request_sha256"`
	ReservationMarker        string `json:"reservation_marker"`
	VersionResolutionSHA256  string `json:"version_resolution_sha256"`
	CoordinationRef          string `json:"coordination_ref"`
	CoordinationClaimPath    string `json:"coordination_claim_path"`
	CoordinationClaimOID     string `json:"coordination_claim_oid"`
	CoordinationClaimBlobOID string `json:"coordination_claim_blob_oid"`
	CoordinationClaimSHA256  string `json:"coordination_claim_sha256"`
	CoordinationParentOID    string `json:"coordination_parent_oid"`
}

type StateV3ActiveRecordEvidence struct {
	Present        bool                           `json:"present"`
	Path           string                         `json:"path,omitempty"`
	Raw            []byte                         `json:"raw,omitempty"`
	SHA256         string                         `json:"sha256,omitempty"`
	BlobOID        string                         `json:"blob_oid,omitempty"`
	StagedEnvelope *StateV3StagedEnvelopeEvidence `json:"staged_envelope,omitempty"`
}

type StateV3StateSnapshot struct {
	Commit         StateV3StateCommitEvidence     `json:"commit"`
	Tree           StateV3StateTreeEvidence       `json:"tree"`
	ActiveRecord   StateV3ActiveRecordEvidence    `json:"active_record"`
	LeasePresent   bool                           `json:"lease_present"`
	LeasePath      string                         `json:"lease_path,omitempty"`
	RawLease       []byte                         `json:"raw_lease,omitempty"`
	LeaseSHA256    string                         `json:"lease_sha256,omitempty"`
	LeaseBlobOID   string                         `json:"lease_blob_oid,omitempty"`
	RecordPresent  bool                           `json:"record_present"`
	RecordPath     string                         `json:"record_path,omitempty"`
	RawRecord      []byte                         `json:"raw_record,omitempty"`
	RecordSHA256   string                         `json:"record_sha256,omitempty"`
	RecordBlobOID  string                         `json:"record_blob_oid,omitempty"`
	StagedEnvelope *StateV3StagedEnvelopeEvidence `json:"staged_envelope,omitempty"`
}

type StateV3StateTreeEvidence struct {
	OID       string                  `json:"oid"`
	Complete  bool                    `json:"complete"`
	Truncated bool                    `json:"truncated"`
	Entries   []StateV3StateTreeEntry `json:"entries"`
}

type StateV3StateTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	OID  string `json:"oid"`
}

type StateV3ChangedPath struct {
	Path      string `json:"path"`
	ParentOID string `json:"parent_oid,omitempty"`
	ChildOID  string `json:"child_oid,omitempty"`
}

type StateV3StateCommitEvidence struct {
	OID                   string               `json:"oid"`
	ParentOID             string               `json:"parent_oid,omitempty"`
	TreeOID               string               `json:"tree_oid"`
	ChangedPaths          []StateV3ChangedPath `json:"changed_paths"`
	RESTVerified          bool                 `json:"rest_verified"`
	RESTReason            string               `json:"rest_reason"`
	GraphQLSignatureValid bool                 `json:"graphql_signature_valid"`
	WasSignedByGitHub     bool                 `json:"was_signed_by_github"`
	SignatureState        string               `json:"signature_state"`
	Roles                 StateV3CommitRoles   `json:"roles"`
}
