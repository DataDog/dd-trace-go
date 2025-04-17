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

- Kubernetes cluster with Gateway API CRDs installed (can be
  done [here](https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api))
- A controller compatible with the Gateway API (list of supported controllers can be
  found [here](https://gateway-api.sigs.k8s.io/implementations)).

### Step 1: Set Up the Datadog Agent

1. [Deploy the Datadog agent in your Kubernetes cluster](https://docs.datadoghq.com/containers/kubernetes/installation/)

2. [Configure the Datadog agent to support incoming Appsec payloads](https://docs.datadoghq.com/tracing/guide/setting_up_apm_with_kubernetes_service/)

3. Deploy the Appsec Gateway API Request in the `datadog` namespace:

  ```bash
  kubectl apply -f https://raw.githubusercontent.com/DataDog/dd-trace-go/main/contrib/k8s.io/gateway-api/cmd/request-mirror/deployment.yaml
  ```

4. Verify the deployment:

  ```bash
  kubectl get pods -n datadog -l app=request-mirror
  ```

5. Make sure your `Gateway` resources allow access to the `datadog` namespace.

  ```bash
  go run github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/cmd/patch-gateways@latest
  ```

6. Make sure your `HTTPRoute` resources allow access to the `datadog` namespace.

  ```bash
  go run github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/cmd/patch-httproutes@latest
  ```
