// Package formatverbsnolint verifies that logformatverbs honors pre-existing
// //nolint:gocritic exceptions inherited from the retired ruleguard rule this
// analyzer replaces — golangci-lint's own nolint processor understands these,
// but a standalone go vet pass does not without this analyzer replicating it.
package formatverbsnolint

import (
	internallog "example.com/fakelog"
)

func exceptedByNolintOnPreviousLine(v any) {
	//nolint:gocritic // legacy exception predating the analyzer migration
	internallog.Error("value: %v", v)
}

func exceptedByBareNolintSameLine(v any) {
	internallog.Error("value: %v", v) //nolint
}

func exceptedByNamedAnalyzerNolint(v any) {
	//nolint:logformatverbs // explicitly excepted
	internallog.Error("value: %v", v)
}

func notExcepted(v any) {
	internallog.Error("value: %v", v) // want "exposes uncontrolled data"
}
