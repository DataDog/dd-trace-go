// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package aggregateopt

import (
	"time"

	"reflect"

	"github.com/mongodb/mongo-go-driver/core/option"
	"github.com/mongodb/mongo-go-driver/core/session"
	"github.com/mongodb/mongo-go-driver/mongo/mongoopt"
)

var aggregateBundle = new(AggregateBundle)

// Aggregate represents all passable params for the aggregate() function.
type Aggregate interface {
	aggregate()
}

// AggregateOption represents the options for the aggregate() function.
type AggregateOption interface {
	Aggregate
	ConvertAggregateOption() option.AggregateOptioner
}

// AggregateSession is the session for the aggregate() function
type AggregateSession interface {
	Aggregate
	ConvertAggregateSession() *session.Client
}

// AggregateBundle is a bundle of Aggregate options
type AggregateBundle struct {
	option Aggregate
	next   *AggregateBundle
}

// Implement the Aggregate interface
func (ab *AggregateBundle) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (ab *AggregateBundle) ConvertAggregateOption() option.AggregateOptioner { return nil }

// BundleAggregate bundles Aggregate options
func BundleAggregate(opts ...Aggregate) *AggregateBundle {
	head := aggregateBundle

	for _, opt := range opts {
		newBundle := AggregateBundle{
			option: opt,
			next:   head,
		}

		head = &newBundle
	}

	return head
}

// AllowDiskUse adds an option to allow aggregation stages to write to temporary files.
func (ab *AggregateBundle) AllowDiskUse(b bool) *AggregateBundle {
	bundle := &AggregateBundle{
		option: AllowDiskUse(b),
		next:   ab,
	}

	return bundle
}

// BatchSize adds an option to specify the number of documents to return in every batch.
func (ab *AggregateBundle) BatchSize(i int32) *AggregateBundle {
	bundle := &AggregateBundle{
		option: BatchSize(i),
		next:   ab,
	}

	return bundle
}

// BypassDocumentValidation adds an option to allow the write to opt-out of document-level validation.
func (ab *AggregateBundle) BypassDocumentValidation(b bool) *AggregateBundle {
	bundle := &AggregateBundle{
		option: BypassDocumentValidation(b),
		next:   ab,
	}

	return bundle
}

// Collation adds an option to specify a Collation.
func (ab *AggregateBundle) Collation(c *mongoopt.Collation) *AggregateBundle {
	bundle := &AggregateBundle{
		option: Collation(c),
		next:   ab,
	}

	return bundle
}

// MaxTime adds an option to specify the maximum amount of time to allow the query to run.
func (ab *AggregateBundle) MaxTime(d time.Duration) *AggregateBundle {
	bundle := &AggregateBundle{
		option: MaxTime(d),
		next:   ab,
	}

	return bundle
}

// MaxAwaitTime adds an option to specify the maximum amount of time for the server to wait on new documents to satisfy a tailable cursor query
func (ab *AggregateBundle) MaxAwaitTime(d time.Duration) *AggregateBundle {
	bundle := &AggregateBundle{
		option: MaxAwaitTime(d),
		next:   ab,
	}

	return bundle
}

// Comment adds an option to specify a string to help trace the operation through the database profiler, currentOp, and logs.
func (ab *AggregateBundle) Comment(s string) *AggregateBundle {
	bundle := &AggregateBundle{
		option: Comment(s),
		next:   ab,
	}

	return bundle
}

// Hint adds an option to specify the index to use for the aggregation.
func (ab *AggregateBundle) Hint(hint interface{}) *AggregateBundle {
	bundle := &AggregateBundle{
		option: Hint(hint),
		next:   ab,
	}

	return bundle
}

// Calculates the total length of a bundle, accounting for nested bundles.
func (ab *AggregateBundle) bundleLength() int {
	if ab == nil {
		return 0
	}

	bundleLen := 0
	for ; ab != nil; ab = ab.next {
		if ab.option == nil {
			continue
		}
		if converted, ok := ab.option.(*AggregateBundle); ok {
			// nested bundle
			bundleLen += converted.bundleLength()
			continue
		}

		if _, ok := ab.option.(AggregateSessionOpt); !ok {
			bundleLen++
		}
	}

	return bundleLen
}

// Unbundle transforms a bundle into a slice of options, optionally deduplicating
func (ab *AggregateBundle) Unbundle(deduplicate bool) ([]option.AggregateOptioner, *session.Client, error) {
	options, sess, err := ab.unbundle()
	if err != nil {
		return nil, nil, err
	}

	if !deduplicate {
		return options, sess, nil
	}

	// iterate backwards and make dedup slice
	optionsSet := make(map[reflect.Type]struct{})

	for i := len(options) - 1; i >= 0; i-- {
		currOption := options[i]
		optionType := reflect.TypeOf(currOption)

		if _, ok := optionsSet[optionType]; ok {
			// option already found
			options = append(options[:i], options[i+1:]...)
			continue
		}

		optionsSet[optionType] = struct{}{}
	}

	return options, sess, nil
}

// Helper that recursively unwraps bundle into slice of options
func (ab *AggregateBundle) unbundle() ([]option.AggregateOptioner, *session.Client, error) {
	if ab == nil {
		return nil, nil, nil
	}

	var sess *session.Client
	listLen := ab.bundleLength()

	options := make([]option.AggregateOptioner, listLen)
	index := listLen - 1

	for listHead := ab; listHead != nil; listHead = listHead.next {
		if listHead.option == nil {
			continue
		}

		// if the current option is a nested bundle, Unbundle it and add its options to the current array
		if converted, ok := listHead.option.(*AggregateBundle); ok {
			nestedOptions, s, err := converted.unbundle()
			if err != nil {
				return nil, nil, err
			}
			if s != nil && sess == nil {
				sess = s
			}

			// where to start inserting nested options
			startIndex := index - len(nestedOptions) + 1

			// add nested options in order
			for _, nestedOp := range nestedOptions {
				options[startIndex] = nestedOp
				startIndex++
			}
			index -= len(nestedOptions)
			continue
		}

		switch t := listHead.option.(type) {
		case AggregateOption:
			options[index] = t.ConvertAggregateOption()
			index--
		case AggregateSession:
			if sess == nil {
				sess = t.ConvertAggregateSession()
			}
		}
	}

	return options, sess, nil
}

// String implements the Stringer interface
func (ab *AggregateBundle) String() string {
	if ab == nil {
		return ""
	}

	str := ""
	for head := ab; head != nil && head.option != nil; head = head.next {
		if converted, ok := head.option.(*AggregateBundle); ok {
			str += converted.String()
			continue
		}

		if conv, ok := head.option.(AggregateOption); !ok {
			str += conv.ConvertAggregateOption().String() + "\n"
		}
	}

	return str
}

// AllowDiskUse allows aggregation stages to write to temporary files.
func AllowDiskUse(b bool) OptAllowDiskUse {
	return OptAllowDiskUse(b)
}

// BatchSize specifies the number of documents to return in every batch.
func BatchSize(i int32) OptBatchSize {
	return OptBatchSize(i)
}

// BypassDocumentValidation allows the write to opt-out of document-level validation.
func BypassDocumentValidation(b bool) OptBypassDocumentValidation {
	return OptBypassDocumentValidation(b)
}

// Collation specifies a collation.
func Collation(c *mongoopt.Collation) OptCollation {
	return OptCollation{
		Collation: c.Convert(),
	}
}

// MaxTime specifies the maximum amount of time to allow the query to run.
func MaxTime(d time.Duration) OptMaxTime {
	return OptMaxTime(d)
}

// MaxAwaitTime specifies the maximum amount of time for the server to wait on new documents to satisfy a tailable cursor query
func MaxAwaitTime(d time.Duration) OptMaxAwaitTime {
	return OptMaxAwaitTime(d)
}

// Comment allows users to specify a string to help trace the operation through the database profiler, currentOp, and logs.
func Comment(s string) OptComment {
	return OptComment(s)
}

// Hint specifies the index to use for the aggregation.
func Hint(hint interface{}) OptHint {
	return OptHint{hint}
}

// OptAllowDiskUse allows aggregation stages to write to temporary files.
type OptAllowDiskUse option.OptAllowDiskUse

func (OptAllowDiskUse) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptAllowDiskUse) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptAllowDiskUse(opt)
}

// OptBatchSize specifies the number of documents to return in every batch.
type OptBatchSize option.OptBatchSize

func (OptBatchSize) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptBatchSize) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptBatchSize(opt)
}

// OptBypassDocumentValidation allows the write to opt-out of document-level validation.
type OptBypassDocumentValidation option.OptBypassDocumentValidation

// ConvertAggregateOption implements the Aggregate interface
func (opt OptBypassDocumentValidation) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptBypassDocumentValidation(opt)
}

func (OptBypassDocumentValidation) aggregate() {}

// OptCollation specifies a collation.
type OptCollation option.OptCollation

func (OptCollation) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptCollation) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptCollation(opt)
}

// OptMaxTime specifies the maximum amount of time to allow the query to run.
type OptMaxTime option.OptMaxTime

func (OptMaxTime) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptMaxTime) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptMaxTime(opt)
}

// OptMaxAwaitTime specifies the maximum amount of time for the server to wait on new documents to satisfy a tailable cursor query
type OptMaxAwaitTime option.OptMaxAwaitTime

func (OptMaxAwaitTime) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptMaxAwaitTime) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptMaxAwaitTime(opt)
}

// OptComment allows users to specify a string to help trace the operation through the database profiler, currentOp, and logs.
type OptComment option.OptComment

func (OptComment) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptComment) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptComment(opt)
}

// OptHint specifies the index to use for the aggregation.
type OptHint option.OptHint

func (OptHint) aggregate() {}

// ConvertAggregateOption implements the Aggregate interface
func (opt OptHint) ConvertAggregateOption() option.AggregateOptioner {
	return option.OptHint(opt)
}

// AggregateSessionOpt is an aggregate session option.
type AggregateSessionOpt struct{}

func (AggregateSessionOpt) aggregate() {}

// ConvertAggregateSession implements the AggregateSession interface.
func (AggregateSessionOpt) ConvertAggregateSession() *session.Client {
	return nil
}
