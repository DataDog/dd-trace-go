#!/bin/bash

# compiles test fixtures
set -e
protoc3 -I . fixtures.proto --go_out=plugins=grpc:.
mv fixtures.pb.go fixtures_test.go
