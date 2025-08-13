# Appsec Gateway API Request Mirror

This document provides a detailed guide for implementing the **Datadog AppSec Gateway API Request Mirror** feature using
the `RequestMirror` functionality of Kubernetes Gateway APIs and Datadog's `dd-trace-go`. The goal is to mirror incoming
HTTP requests to a secondary endpoint for security analysis without affecting the primary request flow.

## Overview

The **Datadog AppSec Gateway API Request Mirror** is designed to enhance application security by mirroring incoming HTTP
requests to a Datadog Application Security Monitoring (ASM) endpoint. This allows real-time detection and analysis of
potential application-level attacks, such as:

- Cross-Site Scripting (XSS)
- SQL Injection (SQLi)
- Server-Side Request Forgery (SSRF)

This feature leverages the **RequestMirror** functionality in Kubernetes Gateway APIs to duplicate traffic to a
secondary server where Datadog's request mirror deployment processes the requests.

## Prerequisites

- A Kubernetes cluster with Gateway API CRDs installed (instructions can be
  found [here](https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api)).
- A controller compatible with the Gateway API (list of supported controllers can be
  found [here](https://gateway-api.sigs.k8s.io/implementations)).
- [Go](https://go.dev/doc/install) 1.24+ installed on your local machine.

## Installation

1. [Deploy the Datadog agent in your Kubernetes cluster](https://docs.datadoghq.com/containers/kubernetes/installation/)

2. [Configure the Datadog agent to support incoming Appsec payloads](https://docs.datadoghq.com/tracing/guide/setting_up_apm_with_kubernetes_service/)

3.

4. Deploy the Appsec Gateway API Request Mirror in the namespace of your choice (e.g., `datadog`) along with its
   service:

  ```bash
  kubectl apply -f https://raw.githubusercontent.com/DataDog/dd-trace-go/main/contrib/k8s.io/gateway-api/cmd/request-mirror/deployment.yaml
  ```

4. Verify the deployment:

  ```bash
  kubectl get pods -l app=request-mirror
  ```

5. Patch your `Gateway` resources so they allow access to the namespace with the deployment (use `-help` flag for
   options).

  ```bash
  go run github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/cmd/patch-gateways@latest
  ```

6. Patch your `HTTPRoute` resources to redirect traffic to the service.
   This will add a [`RequestMirror`](https://gateway-api.sigs.k8s.io/guides/http-request-mirroring/) filter to all
   `HTTPRoute` resources found in all namespace (use `-help` flag for options). Running this command regularly will
   ensure that any new `HTTPRoute` resources created in the future will also have the `RequestMirror`
   filter added. Adding the resulting patch to your CI/CD where HTTPRoute are modified is recommended in the long run.

  ```bash
  go run github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/cmd/patch-httproutes@latest
  ```

## Deployment Configuration

The following environment variables are used to configure the Datadog AppSec Gateway API Request Mirror:

| Environment Variable                 | Default Value | Description                                                                                                                |
|--------------------------------------|---------------|----------------------------------------------------------------------------------------------------------------------------|
| `DD_REQUEST_MIRROR_LISTEN_ADDR`      | `:8080`       | Value passed in to [net/http.ListenAndServe](https://pkg.go.dev/net/http#ListenAndServe) to receive requests               |
| `DD_REQUEST_MIRROR_HEALTHCHECK_ADDR` | `:8081`       | Value passed in to [net/http.ListenAndServe](https://pkg.go.dev/net/http#ListenAndServe) to listen to healthcheck requests |

By default, the request mirror traces won't enable the Datadog's APM product. It can be enabled using the env var
`DD_APM_TRACING_ENABLED=true`
