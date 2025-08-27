#!/bin/bash
set -euo pipefail

OUT_DIR=$1
cd "${OUT_DIR}"

YEAR=$(date +'%Y')
COPYRIGHT_HEADER="// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright ${YEAR} Datadog, Inc.
"

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
export PATH="${SCRIPT_DIR}/../../../../bin:${PATH}"

protoc fixture.proto \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative

for f in ./*.pb.go; do
  printf "%s\n%s" "$COPYRIGHT_HEADER" "$(cat "$f")" > "$f"
  go fmt "$f"
done
