// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package profiler periodically collects and sends profiles to the Datadog API.
// Use Start to start the profiler.
package profiler // import "gopkg.in/DataDog/dd-trace-go.v1/profiler"

/*
Developer documentation:

The Go profiler client library works as follows:

	* The user configures and initiates profile collection by calling the
	  profiler.Start function, typically in main(). Collection
	  starts asynchronously, so profiler.Start will return quickly and the
	  actual data collection is done in the background.
	* The user calls profiler.Stop at the end of the program to end profile
	  collection and uploading. This is typically done via a defer statement
	  in main() after calling profiler.Start.
	* The profiler initiates two goroutines: one which will collect the
	  configured profiles every minute, and another one which receives profile
	  batches from the first goroutine and sends them to the agent. The
	  collection and uploading processes are separate so that delays in
	  uploading to not prevent profile collection.
	* The collection goroutine is a for loop, which collects each profile
	  type (see profile.go for the implementations), batches them together,
	  and passes the batch to a channel which will be read by the upload
	  goroutine, and then waits until the next minute to collect again. The
	  loop also checks whether the profiler is stopped by checking if the
	  p.exit channel is closed.
	* The upload goroutine loops, each iteration waiting to receive a batch
	  of profiles or for p.exit to be closed. Upon receiving a batch, the
	  upload loop calls an upload function. This function constructs the
	  message containing the profiles and metadata, and attempts to upload it
	  to the agent's profile proxy. This will be retried several times.

The code is laid out in the following files:

	* profiler.go: contains the implementation of the primary profiler
	  object, which holds the configuration and manages profile collection.
	* profile.go: contains the implementation of profile collection for
	  specific profile types.  All profile types are collected via Go runtime
	  APIs, and some require post-processing for our use. The primary kind of
	  post-processing is computing "deltas", which are the difference between
	  two profiles to show the change in resource usage (memory allocations,
	  blocking time, etc) over the last minute. Delta computation is in
	  "internal/fastdelta".
	* upload.go: implements uploading a batch of profiles to our agent's
	  backend proxy, including bundling them together in the required
	  multi-part form layout and adding required metadata such as tags.
	* options.go: implements configuration logic, including default values
	  and functional options which are passed to profiler.Start.
	* telemetry.go: sends an instrumentation telemetry message containing
	  profiler configuration when profiling is started.
	* metrics.go: collects some runtime metrics (GC-related) which are
	  included in the metrics.json attachment for each profile upload.

The code is tested in the "*_test.go" files. The profiler implementations
themselves are in the Go standard library, and are tested for correctness there.
The tests for this library are concerned with ensuring the expected behavior of
the client: Do we collect the configured profiles? Do we add the expected
metadata? Do we respect collection intervals? Are our manipulations of the
profiles (such as delta computation) correct? The test also include regression
tests from previously-identified bugs in the implementation.

*/
