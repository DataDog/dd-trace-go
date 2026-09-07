// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type prereleaseKind string

const (
	prereleaseNone prereleaseKind = ""
	prereleaseRC   prereleaseKind = "rc"
	prereleaseDev  prereleaseKind = "dev"
)

type releaseVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease prereleaseKind
	Number     int
	BareDev    bool
}

type RemoteRefs struct {
	Complete              bool
	Branches              map[string]string
	Tags                  map[string]string
	IncompleteTagVersions []string
}

type ExistingOperation struct {
	RequestKey         string
	RequestSHA256      string
	Command            string
	ReleaseLine        string
	ResolvedVersion    string
	DevelopmentVersion string
	Phase              string
}

type VersionResolutionInput struct {
	RequestKey         string
	RequestSHA256      string
	Command            string
	RequestedVersion   string
	ReleaseLine        string
	SourceVersion      string
	RemoteRefs         RemoteRefs
	ExistingOperations []ExistingOperation
}

type VersionResolution struct {
	Resume             bool   `json:"resume"`
	Command            string `json:"command"`
	ReleaseLine        string `json:"release_line"`
	SourceVersion      string `json:"source_version"`
	RequestedVersion   string `json:"requested_version"`
	ResolvedVersion    string `json:"resolved_version"`
	DevelopmentVersion string `json:"development_version,omitempty"`
	ReleaseBranch      string `json:"release_branch,omitempty"`
	DevelopmentBranch  string `json:"development_branch,omitempty"`
}

func ParseReleaseVersion(raw string) (releaseVersion, error) {
	match := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(rc)\.([1-9][0-9]*)|-(dev)(?:\.([1-9][0-9]*))?)?$`).FindStringSubmatch(raw)
	if match == nil {
		return releaseVersion{}, typedRequestError("invalid_version")
	}
	major, err := parseVersionComponent(match[1])
	if err != nil {
		return releaseVersion{}, err
	}
	minor, err := parseVersionComponent(match[2])
	if err != nil {
		return releaseVersion{}, err
	}
	patch, err := parseVersionComponent(match[3])
	if err != nil {
		return releaseVersion{}, err
	}
	version := releaseVersion{Major: major, Minor: minor, Patch: patch}
	if match[4] == "rc" {
		number, err := parseVersionComponent(match[5])
		if err != nil {
			return releaseVersion{}, err
		}
		version.Prerelease = prereleaseRC
		version.Number = number
	}
	if match[6] == "dev" {
		version.Prerelease = prereleaseDev
		version.BareDev = match[7] == ""
		if match[7] != "" {
			number, err := parseVersionComponent(match[7])
			if err != nil {
				return releaseVersion{}, err
			}
			version.Number = number
		}
	}
	return version, nil
}

func ResolveVersion(input VersionResolutionInput) (VersionResolution, error) {
	if !isReleaseCommand(input.Command) {
		return VersionResolution{}, typedRequestError("unknown_release_command")
	}
	line, err := parseReleaseLine(input.ReleaseLine)
	if err != nil {
		return VersionResolution{}, err
	}
	if input.RequestedVersion == "" {
		return VersionResolution{}, typedRequestError("invalid_version")
	}
	for _, record := range input.ExistingOperations {
		if record.RequestKey != input.RequestKey {
			continue
		}
		if record.RequestSHA256 != input.RequestSHA256 {
			return VersionResolution{}, typedStateError("request_hash_conflict")
		}
		if record.ResolvedVersion == "" || record.ReleaseLine != input.ReleaseLine || record.Command != input.Command {
			return VersionResolution{}, typedStateError("invalid_record")
		}
		return VersionResolution{
			Resume:             true,
			Command:            record.Command,
			ReleaseLine:        record.ReleaseLine,
			SourceVersion:      input.SourceVersion,
			RequestedVersion:   input.RequestedVersion,
			ResolvedVersion:    record.ResolvedVersion,
			DevelopmentVersion: record.DevelopmentVersion,
			ReleaseBranch:      releaseBranchName(line.Major, line.Minor),
		}, nil
	}
	for _, record := range input.ExistingOperations {
		if record.ReleaseLine == input.ReleaseLine && !operationComplete(record.Phase) {
			return VersionResolution{}, typedStateError("incomplete_operation")
		}
	}
	if !input.RemoteRefs.Complete {
		return VersionResolution{}, typedEvidenceError("remote_refs_incomplete")
	}
	switch input.Command {
	case "release:prepare":
		return resolvePrepare(input, line)
	case "release:promote":
		return resolvePromote(input, line)
	case "release:release":
		return resolveRelease(input, line)
	default:
		return VersionResolution{}, typedRequestError("unknown_release_command")
	}
}

func resolvePrepare(input VersionResolutionInput, line releaseVersion) (VersionResolution, error) {
	target := releaseVersion{Major: line.Major, Minor: line.Minor, Patch: 0}
	if input.RequestedVersion != "auto" {
		requested, err := ParseReleaseVersion(input.RequestedVersion)
		if err != nil {
			return VersionResolution{}, err
		}
		if requested.Prerelease != prereleaseNone || requested.Major != line.Major || requested.Minor != line.Minor {
			return VersionResolution{}, typedRequestError("line_mismatch")
		}
		if requested.Patch != 0 {
			return VersionResolution{}, typedRequestError("prepare_patch_not_zero")
		}
		target = requested
	}
	source, err := ParseReleaseVersion(input.SourceVersion)
	if err != nil {
		return VersionResolution{}, err
	}
	if source.Prerelease != prereleaseDev || source.Major != line.Major || source.Minor != line.Minor || source.Patch != 0 {
		return VersionResolution{}, typedRequestError("source_line_mismatch")
	}
	releaseBranch := releaseBranchName(line.Major, line.Minor)
	devBranch := devBranchName(line.Major, line.Minor+1)
	if hasBranch(input.RemoteRefs, releaseBranch) || hasBranch(input.RemoteRefs, devBranch) {
		return VersionResolution{}, typedStateError("branch_exists_without_record")
	}
	devVersion := nextDevelopmentVersion(line.Major, line.Minor+1, input.RemoteRefs)
	return VersionResolution{
		Command:            input.Command,
		ReleaseLine:        input.ReleaseLine,
		SourceVersion:      input.SourceVersion,
		RequestedVersion:   input.RequestedVersion,
		ResolvedVersion:    target.String(),
		DevelopmentVersion: devVersion.String(),
		ReleaseBranch:      releaseBranch,
		DevelopmentBranch:  devBranch,
	}, nil
}

func resolvePromote(input VersionResolutionInput, line releaseVersion) (VersionResolution, error) {
	source, err := ParseReleaseVersion(input.SourceVersion)
	if err != nil {
		return VersionResolution{}, err
	}
	if source.Major != line.Major || source.Minor != line.Minor {
		return VersionResolution{}, typedRequestError("source_line_mismatch")
	}
	base, minRC, err := promotionBase(input.RequestedVersion, source, line)
	if err != nil {
		return VersionResolution{}, err
	}
	if input.RemoteRefs.hasTag(base.String()) {
		return VersionResolution{}, typedStateError("existing_ga")
	}
	if input.RemoteRefs.incompleteVersion(base.String()) {
		return VersionResolution{}, typedStateError("incomplete_tags")
	}
	resolved := nextRCVersion(base, minRC, input.RemoteRefs)
	return VersionResolution{
		Command:          input.Command,
		ReleaseLine:      input.ReleaseLine,
		SourceVersion:    input.SourceVersion,
		RequestedVersion: input.RequestedVersion,
		ResolvedVersion:  resolved.String(),
		ReleaseBranch:    releaseBranchName(line.Major, line.Minor),
	}, nil
}

func resolveRelease(input VersionResolutionInput, line releaseVersion) (VersionResolution, error) {
	source, err := ParseReleaseVersion(input.SourceVersion)
	if err != nil {
		return VersionResolution{}, err
	}
	if source.Prerelease != prereleaseRC || source.Major != line.Major || source.Minor != line.Minor {
		return VersionResolution{}, typedRequestError("source_line_mismatch")
	}
	base := source.base()
	if input.RequestedVersion != "auto" {
		requested, err := ParseReleaseVersion(input.RequestedVersion)
		if err != nil {
			return VersionResolution{}, err
		}
		if requested.Prerelease != prereleaseNone || requested.Major != line.Major || requested.Minor != line.Minor {
			return VersionResolution{}, typedRequestError("line_mismatch")
		}
		if compareBase(requested, base) != 0 {
			return VersionResolution{}, typedRequestError("source_line_mismatch")
		}
		base = requested
	}
	if input.RemoteRefs.hasTag(base.String()) {
		return VersionResolution{}, typedStateError("existing_ga")
	}
	if input.RemoteRefs.hasHigherGA(base) {
		return VersionResolution{}, typedRequestError("ga_below_highest")
	}
	if input.RemoteRefs.incompleteVersion(base.String()) {
		return VersionResolution{}, typedStateError("incomplete_tags")
	}
	return VersionResolution{
		Command:          input.Command,
		ReleaseLine:      input.ReleaseLine,
		SourceVersion:    input.SourceVersion,
		RequestedVersion: input.RequestedVersion,
		ResolvedVersion:  base.String(),
		ReleaseBranch:    releaseBranchName(line.Major, line.Minor),
	}, nil
}

func promotionBase(requested string, source, line releaseVersion) (releaseVersion, int, error) {
	if requested == "auto" {
		switch source.Prerelease {
		case prereleaseDev:
			return source.base(), 1, nil
		case prereleaseRC:
			return source.base(), source.Number + 1, nil
		default:
			return releaseVersion{}, 0, typedRequestError("stable_auto_rejected")
		}
	}
	base, err := ParseReleaseVersion(requested)
	if err != nil {
		return releaseVersion{}, 0, err
	}
	if base.Prerelease != prereleaseNone || base.Major != line.Major || base.Minor != line.Minor {
		return releaseVersion{}, 0, typedRequestError("line_mismatch")
	}
	cmp := compareBase(base, source.base())
	if cmp < 0 {
		return releaseVersion{}, 0, typedRequestError("version_downgrade")
	}
	if source.Prerelease == prereleaseNone && cmp == 0 {
		return releaseVersion{}, 0, typedRequestError("stable_branch_requires_newer_patch")
	}
	minRC := 1
	if source.Prerelease == prereleaseRC && cmp == 0 {
		minRC = source.Number + 1
	}
	return base, minRC, nil
}

func nextRCVersion(base releaseVersion, minRC int, refs RemoteRefs) releaseVersion {
	maxRC := minRC - 1
	for tag := range refs.Tags {
		candidate, err := ParseReleaseVersion(strings.TrimPrefix(tag, "refs/tags/"))
		if err != nil || candidate.Prerelease != prereleaseRC || compareBase(candidate, base) != 0 {
			continue
		}
		if candidate.Number > maxRC {
			maxRC = candidate.Number
		}
	}
	base.Prerelease = prereleaseRC
	base.Number = maxRC + 1
	return base
}

func nextDevelopmentVersion(major, minor int, refs RemoteRefs) releaseVersion {
	base := releaseVersion{Major: major, Minor: minor, Patch: 0, Prerelease: prereleaseDev, BareDev: true}
	if !refs.hasTag(base.String()) {
		return base
	}
	maxDev := 0
	for tag := range refs.Tags {
		candidate, err := ParseReleaseVersion(strings.TrimPrefix(tag, "refs/tags/"))
		if err != nil || candidate.Prerelease != prereleaseDev || candidate.BareDev || candidate.Major != major || candidate.Minor != minor || candidate.Patch != 0 {
			continue
		}
		if candidate.Number > maxDev {
			maxDev = candidate.Number
		}
	}
	return releaseVersion{Major: major, Minor: minor, Patch: 0, Prerelease: prereleaseDev, Number: maxDev + 1}
}

func parseReleaseLine(line string) (releaseVersion, error) {
	match := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).FindStringSubmatch(line)
	if match == nil {
		return releaseVersion{}, typedContractError("policy_drift")
	}
	major, err := parseVersionComponent(match[1])
	if err != nil {
		return releaseVersion{}, err
	}
	minor, err := parseVersionComponent(match[2])
	if err != nil {
		return releaseVersion{}, err
	}
	return releaseVersion{Major: major, Minor: minor}, nil
}

func parseVersionComponent(component string) (int, error) {
	value, err := strconv.ParseInt(component, 10, 64)
	if err != nil || value > MaxVersionComponent {
		return 0, typedRequestError("version_overflow")
	}
	return int(value), nil
}

func (v releaseVersion) String() string {
	base := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	switch v.Prerelease {
	case prereleaseRC:
		return fmt.Sprintf("%s-rc.%d", base, v.Number)
	case prereleaseDev:
		if v.BareDev {
			return base + "-dev"
		}
		return fmt.Sprintf("%s-dev.%d", base, v.Number)
	default:
		return base
	}
}

func (v releaseVersion) base() releaseVersion {
	return releaseVersion{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
}

func compareBase(a, b releaseVersion) int {
	if a.Major != b.Major {
		return compareInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return compareInt(a.Minor, b.Minor)
	}
	return compareInt(a.Patch, b.Patch)
}

func compareInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func hasBranch(refs RemoteRefs, branch string) bool {
	if refs.Branches == nil {
		return false
	}
	_, ok := refs.Branches["refs/heads/"+branch]
	if ok {
		return true
	}
	_, ok = refs.Branches[branch]
	return ok
}

func (refs RemoteRefs) hasTag(tag string) bool {
	if refs.Tags == nil {
		return false
	}
	_, ok := refs.Tags[tag]
	if ok {
		return true
	}
	_, ok = refs.Tags["refs/tags/"+tag]
	return ok
}

func (refs RemoteRefs) hasHigherGA(base releaseVersion) bool {
	for tag := range refs.Tags {
		name := strings.TrimPrefix(tag, "refs/tags/")
		if strings.Contains(name, "/") {
			continue
		}
		candidate, err := ParseReleaseVersion(name)
		if err != nil || candidate.Prerelease != prereleaseNone {
			continue
		}
		if compareBase(candidate, base) > 0 {
			return true
		}
	}
	return false
}

func (refs RemoteRefs) incompleteVersion(version string) bool {
	for _, incomplete := range refs.IncompleteTagVersions {
		if incomplete == version {
			return true
		}
	}
	return false
}

func releaseBranchName(major, minor int) string {
	return fmt.Sprintf("release-v%d.%d.x", major, minor)
}

func devBranchName(major, minor int) string {
	return fmt.Sprintf("dev-v%d.%d.x", major, minor)
}

func operationComplete(phase string) bool {
	return phase == "complete"
}

func typedStateError(code string) *ReleaseError {
	return newReleaseError(ErrorClassStateConflict, code)
}

func typedEvidenceError(code string) *ReleaseError {
	return newReleaseError(ErrorClassEvidenceIncomplete, code)
}
