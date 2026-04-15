# RFC: Trace Context Propagation Extraction Modes

Author: [Zach Montoya](mailto:zach.montoya@datadoghq.com)  
Date: Dec 4, 2024  
Status: Approved  
Previous Proposal(s): [Proposal: Fixing Trace Context Propagation Across Organizations](https://docs.google.com/document/d/1htJFBWR4RFpmK1i6wopxkZwlvYu9unkPJnMnTom4Xjo/edit?usp=sharing)  
Problem Statement: [Escalation Review: Handling cases where the root service is an unrelated/unknown upstream service](https://docs.google.com/document/d/1zDQDQIRkW8OhTGZ1ua8IsGG_mf5MLdMoUYiAmn9G0pE/edit?tab=t.0)  
Feature Parity Dashboard: [https://feature-parity.us1.prod.dog/\#/feature-health?viewType=tests\&features=353](https://feature-parity.us1.prod.dog/#/feature-health?viewType=tests&features=353) 

| Reviewer | Status | Notes |
| :---- | :---- | :---- |
| [Zach Groves](mailto:zach.groves@datadoghq.com) | Approved |  |
| [Lucas Pimentel](mailto:lucas.pimentel@datadoghq.com) | Approved |  |
| [Mikayla Toffler](mailto:mikayla.toffler@datadoghq.com) | Approved |  |
| [Matthew Li](mailto:matthew.li@datadoghq.com) | Approved |  |

# Overview

## Motivation

As a result of increased adoption of W3C Trace Context (and possibly just increased adoption of Datadog APM), the APM Ecosystems engineering teams have observed an increasing number of customer escalations where context is propagated from the service of one organization into the service of another organization, leading to a degraded APM experience for the second organization. More context on these issues can be found here: [Escalation Review: Handling cases where the root service is an unrelated/unknown upstream service](https://docs.google.com/document/d/1zDQDQIRkW8OhTGZ1ua8IsGG_mf5MLdMoUYiAmn9G0pE/edit?tab=t.0#heading=h.fna027uqmyrt).

We should provide our customers with tools to resolve this situation, specifically on the context extraction side as the changes can immediately remedy the situation for the downstream customer. We currently have one workaround which customers have successfully used, which is to configure DD\_TRACE\_PROPAGATION\_STYLE\_EXTRACT=none so that no incoming distributed tracing headers are checked for context extraction, but this solution is not extensible and conflates propagator formats with the context extraction logic. To provide an improved and more extensible user experience, we should add a new capability to the tracing library that separates the handling of incoming trace contexts from the selected propagator formats.

Note: This work can also be extended to limit context injection from the upstream service, however its effects are not visible to the user so it seems prudent to first focus on the context extraction side.

## Requirements

1. By only updating the Datadog configuration of an instrumented service, a user can ignore any incoming trace context information

# Out of scope

* This RFC specifically doesn’t focus on an end-to-end UI workflow, and instead focuses on implementing the required fundamental behaviors in our tracing libraries that would enable such changes.  
* Benchmarking is also out of scope, as the processing overhead is minimal in comparison to the rest of the context extraction operation.

# Proposed Solution

## Configuration

Environment Variable: DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT  
Accepted Values:

* continue: The tracing library continues the trace from the incoming headers, if present. Also, incoming baggage is propagated.  
* restart: The tracing library always starts a new trace with a new trace-id (and a new sampling decision). Context extraction will occur as otherwise configured, except that the local span will no longer have a parent-child span relationship with the incoming trace context, instead it will reference the incoming trace context via a span link. Also, incoming baggage is propagated.  
* ignore: The tracing library always starts a new trace with a new trace-id (and a new sampling decision) *without creating any span links.* Also, incoming baggage is dropped.

Default value: continue  
Telemetry Key: DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT

## Behavior

This behavior is intricately linked to the [existing context extraction behaviors](https://docs.google.com/document/d/1xacBonCyuVk95D-L1STXGqdLtsOuf0_3jG-138uXHnQ/edit?tab=t.0#bookmark=id.e5pi9gdhj04t). This RFC proposes adding the following text to the existing logic:

1. \[*Modified*\] Iterate through the propagators in precedence order and for each propagator apply the following logic to build the **incoming trace context** from the distributed tracing headers …  
2. \[*New*\] Using the **incoming trace context** from the previous step, set the **local trace context** based on the configured value of DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT:  
   1. If DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=continue, use the **incoming trace context** as the **local trace context.**  
   2. If DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=restart, create a new **local trace context** containing the following information:  
      1. The trace-id and span-id is set in such a way that the tracing library will generate a new trace-id and span-id when starting a new span from this trace context.  
      2. A span link with the following properties:  
         1. The TraceId, SpanId, TraceFlags, and TraceState fields that correspond to the **incoming trace context**  
         2. Additionally, set the following Attributes on the span link:  
            1. Key=reason, Value=propagation\_behavior\_extract  
            2. Key=context\_headers, Value=\<Name of the propagator that initialized the context\>  
               1. Implementation detail: If multiple propagators read the same trace-id and were consolidated into one trace context, listing the first propagator is sufficient rather than listing all of the propagators.  
      3. All baggage items from the **incoming trace context**  
         1. Note: Span links from the incoming trace context do not need to be carried over.  
   3. If DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=ignore, discard the entire **incoming trace context** and create a new **local trace context**. The result will be identical to no distributed tracing headers being found.  
      1. Note: As a performance improvement, it is acceptable to skip the iteration of propagators when this configuration is detected since the contents of distributed tracing headers will not affect the results.

# Testing

Testing for this feature will be asserted through system-tests, and the new tests can be found [here](https://github.com/DataDog/system-tests/pull/3602). In order to pass the test cases, tracing libraries must implement the DD\_TRACE\_PROPAGATION\_EXTRACT\_FIRST configuration and the ability to add span links for **conflicting trace contexts** where the trace-ids do not match the **incoming trace context**. These behaviors are both outlined [here](https://docs.google.com/document/d/1xacBonCyuVk95D-L1STXGqdLtsOuf0_3jG-138uXHnQ/edit?tab=t.0#heading=h.4jm220mfuuo1). For the tests outlined below, we are assuming that the tracing library under test is configured with default extraction propagators: datadog,tracecontext.

## Test Behavior

* The weblog /make\_distant\_call endpoint is invoked with valid Datadog trace context headers, W3C Trace Context headers, and W3C Baggage headers. The Datadog tracing library should automatically perform context extraction and generate a span for this HTTP server request.  
* The HTTP endpoint then creates an outbound HTTP request. The request and response headers for this request are sent in the response body for the original endpoint call. The Datadog tracing library should automatically perform context injection based on the local context.

## Test Cases

There are two test cases for each configuration:

1. The distributed tracing headers share the same trace-id and span-id.  
2. The distributed tracing headers have unique trace-ids and span-ids.

Each test case will be run against the following tracing configurations. Due to the overhead of creating new test scenarios in the system-tests infrastructure, this omits testing the explicit configuration DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=continue. However, it is the default behavior, so it is still indirectly tested here.

1. Default (equivalent to DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=continue)  
2. DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=restart  
3. DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=restart and DD\_TRACE\_PROPAGATION\_EXTRACT\_FIRST=true  
4. DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=ignore

## Test Results

* Configuration: Default  
  1. Test case: Same trace-id  
     1. The HTTP server span has the same trace-id as the incoming Datadog trace context (i.e. the trace is continued).  
     2. The HTTP server span has zero span links.  
     3. Baggage is propagated.  
  2. Test case: Unique trace-ids  
     1. The HTTP server span has the same trace-id as the incoming Datadog trace context (i.e. the trace is continued).  
     2. The HTTP server span has one span link, corresponding to the **conflicting trace context** in the W3C Trace Context headers.  
     3. Baggage is propagated.  
* Configuration: DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=restart  
  1. Test case: Same trace-id  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has one span link, corresponding to the **incoming trace context** extracted from the Datadog and W3C Trace Context.  
     3. Baggage is propagated.  
  2. Test case: Unique trace-ids  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has one span link, corresponding to the **incoming trace context** extracted from the Datadog headers.  
     3. Baggage is propagated.  
* Configuration: DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=ignore  
  1. Test case: Same trace-id  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has no span links.  
     3. Baggage is not propagated.  
  2. Test case: Unique trace-ids  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has no span links.  
     3. Baggage is not propagated.  
* Configuration: DD\_TRACE\_PROPAGATION\_BEHAVIOR\_EXTRACT=restart and DD\_TRACE\_PROPAGATION\_EXTRACT\_FIRST=true  
  1. Test case: Same trace-id  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has one span link, corresponding to the **incoming trace context** extracted from the Datadog headers.  
     3. Baggage is propagated.  
  2. Test case: Unique trace-ids  
     1. The HTTP server span has a new trace-id (i.e. a new trace is started).  
     2. The HTTP server span has one span link, corresponding to the **incoming trace context** extracted from the Datadog headers. No **conflicting trace contexts** are identified because the extraction stops immediately, due to the configuration.  
     3. Baggage is propagated.

# Alternative Solutions

All of the ideas explored in the [Longer Term Options](https://docs.google.com/document/d/1zDQDQIRkW8OhTGZ1ua8IsGG_mf5MLdMoUYiAmn9G0pE/edit?tab=t.0#heading=h.zcquizxtg7t9) section of [Escalation Review: Handling cases where the root service is an unrelated/unknown upstream service](https://docs.google.com/document/u/0/d/1zDQDQIRkW8OhTGZ1ua8IsGG_mf5MLdMoUYiAmn9G0pE/edit) fall into the following camps:

* Sampling  
  * Option 1: Sampling Overrides  
  * Option 5: Implement non-”ParentBased” Samplers in line with OpenTelemetry  
* Filter the context extraction  
  * Option 2: Designate a Head Service / Ignore Incoming Information (this one\!)  
  * Option 6: Implement configuration to filter extraction by host/headers (suggested in [Future Work](#future-work))

## Sampling-based solutions

Sampling-based solutions don’t entirely solve the issue. With sampling, the customer can override the upstream sampling decision so they regain control of their ingestion, but it leaves their spans in an orphaned state, which causes a degraded UI experience.

# Future Work {#future-work}

There are several opportunities to expand on this feature:

1. **Configuring the injection operation:** With this capability, customers could stop the propagation of their own trace context & baggage so that they don’t expose information to untrusted parties. This also prevents the issues being solved here by having upstream services remove unrecognized distributed tracing headers. However, this would also need to be applied to specific services so that users don’t break up their own traces.  
2. **Datadog tracing library dynamically configures the propagation behavior per request:** Since the Datadog tracing library can see incoming/outgoing requests and is likely configured at known ingress/egress points, we may be able to configure this automatically to allow extraction (or injection, see above) to be dynamically configured so that we handle this issue automatically for customers, without them having to set configurations. The capability introduced in this RFC opens the door for those intelligent solutions, such as the alternative solution to filter extraction by host/headers).

# Questions

Ask away\!

# References

* [Escalation Review: Handling cases where the root service is an unrelated/unknown upstream service](https://docs.google.com/document/u/0/d/1zDQDQIRkW8OhTGZ1ua8IsGG_mf5MLdMoUYiAmn9G0pE/edit)  
* [Proposal: Fixing Trace Context Propagation Across Organizations](https://docs.google.com/document/u/0/d/1htJFBWR4RFpmK1i6wopxkZwlvYu9unkPJnMnTom4Xjo/edit)
