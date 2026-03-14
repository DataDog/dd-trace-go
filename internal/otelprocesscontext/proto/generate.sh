#!/usr/bin/env bash

# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026 Datadog, Inc.

set -eu

# This script generates Go code from the ProcessContext protobuf definition.
# It requires protoc and protoc-gen-go to be installed.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

# Create temporary directory for OpenTelemetry proto definitions
OTEL_PROTO_DIR="$(mktemp -d)"
trap "rm -rf '${OTEL_PROTO_DIR}'" EXIT

echo "Cloning OpenTelemetry proto definitions to temporary directory..."
git clone --depth 1 --branch v1.9.0 \
    https://github.com/open-telemetry/opentelemetry-proto.git \
    "${OTEL_PROTO_DIR}" 2>/dev/null

# Generate Go code
echo -n "Generating ProcessContext protobuf code..."
cd "${REPO_ROOT}"

protoc \
    --go_out=internal/otelprocesscontext \
    --go_opt=paths=source_relative \
    "--go_opt=Mprocesscontext.proto=github.com/DataDog/dd-trace-go/v2/internal/otelprocesscontext;otelprocesscontext" \
    "--go_opt=Mopentelemetry/proto/common/v1/common.proto=go.opentelemetry.io/proto/slim/otlp/common/v1;v1" \
    "--go_opt=Mopentelemetry/proto/resource/v1/resource.proto=go.opentelemetry.io/proto/slim/otlp/resource/v1;v1" \
    --proto_path="${OTEL_PROTO_DIR}" \
    --proto_path=internal/otelprocesscontext/proto \
    processcontext.proto

echo " done"
