#!/bin/sh

YEAR=$(date +'%Y')
COPYRIGHT_HEADER="// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright ${YEAR} Datadog, Inc.
"

go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2

protoc fixtures_test.proto \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative

for f in ./*.pb.go; do
  printf "%s\n%s" "$COPYRIGHT_HEADER" "$(cat "$f")" > "$f"
done
