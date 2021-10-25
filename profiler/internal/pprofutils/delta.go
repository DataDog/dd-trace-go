// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package pprofutils

import (
	"errors"

	"github.com/google/pprof/profile"
)

// Delta describes how to compute the delta between two profiles and implements
// the conversion.
type Delta struct {
	// SampleTypes limits the delta calcultion to the given sample types. Other
	// sample types will retain the values of profile b. The defined sample types
	// must exist in the profile, otherwise derivation will fail with an error.
	// If the slice is empty, all sample types are subject to delta profile
	// derivation.
	//
	// The use case for this for this is to deal with the heap profile which
	// contains alloc and inuse sample types, but delta profiling makes no sense
	// for the latter.
	SampleTypes []ValueType
}

// Convert computes the delta between all values b-a and returns them as a new
// profile. Samples that end up with a delta of 0 are dropped. WARNING: Profile
// a will be mutated by this function. You should pass a copy if that's
// undesirable.
func (d Delta) Convert(a, b *profile.Profile) (*profile.Profile, error) {
	ratios := make([]float64, len(a.SampleType))

	found := 0
	for i, st := range a.SampleType {
		// Empty c.SampleTypes means we calculate the delta for every st
		if len(d.SampleTypes) == 0 {
			ratios[i] = -1
			continue
		}

		// Otherwise we only calcuate the delta for any st that is listed in
		// c.SampleTypes. st's not listed in there will default to ratio 0, which
		// means we delete them from pa, so only the pb values remain in the final
		// profile.
		for _, deltaSt := range d.SampleTypes {
			if deltaSt.Type == st.Type && deltaSt.Unit == st.Unit {
				ratios[i] = -1
				found++
			}
		}
	}
	if found != len(d.SampleTypes) {
		return nil, errors.New("one or more sample type(s) was not found in the profile")
	}

	a.ScaleN(ratios)

	delta, err := profile.Merge([]*profile.Profile{a, b})
	if err != nil {
		return nil, err
	}
	fixNegativeValues(delta)
	return delta, delta.CheckValid()
}

// fixNegativeValues removes all samples containing negative values from the
// given delta profile. For each negative sample it checks if a positive sample
// with the same stack trace (program counters) exists, and if yes applies the
// negative values from the removed sample to the matching positive sample.
// This should provide a workaround for PROF-4239 where stack traces with the
// same program counters sometimes end up with slightly different symbolization
// (e.g. different file names). The root cause for this is probably a bug in
// the Go runtime, but this hasn't been confirmed/reproduced in a standalone
// program yet.
func fixNegativeValues(delta *profile.Profile) {
	samples := map[sampleKey]*profile.Sample{}
	newSamples := make([]*profile.Sample, 0, len(delta.Sample))
	for _, s := range delta.Sample {
		key := makeSampleKey(s)
		prevSample := samples[key] // last sample seen with the same stack trace

		if !hasNegativeValue(s) {
			newSamples = append(newSamples, s)
			if prevSample != nil {
				fixSample(s, prevSample)
			}
		} else if prevSample != nil && !hasNegativeValue(prevSample) {
			fixSample(prevSample, s)
		}
		samples[key] = s
	}
	delta.Sample = newSamples
}

// fixSample adds all negative values from the neg sample to the pos sample.
func fixSample(pos, neg *profile.Sample) {
	for i, v := range neg.Value {
		if v < 0 {
			pos.Value[i] += v
		}
	}
}

// makeSampleKey creates a key for a sample using its program counters.
func makeSampleKey(s *profile.Sample) (k sampleKey) {
	for i, l := range s.Location {
		k[i] = l.Address
	}
	return
}

// sampleKey holds the program counters of a stack trace. Memory/Block/Mutex
// profiles have a max-depth of 32 right now which is unlikely to change, but
// 256 should give enough safety margin.
type sampleKey [256]uint64

// hasNegativeValue returns true if one or more
func hasNegativeValue(s *profile.Sample) bool {
	for _, v := range s.Value {
		if v < 0 {
			return true
		}
	}
	return false
}
