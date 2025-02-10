package internal

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

// Logger is the logger used in this package, intended to be assigned from the
// instrumentation package logger loaded at init time in `aws`.
// This is created to avoid registering more `aws-sdk-go-v2` contribs, as Serverless
// requires only
var Logger instrumentation.Logger
