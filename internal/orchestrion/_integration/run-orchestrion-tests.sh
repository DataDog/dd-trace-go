#!/bin/bash

set -eu

orchestrion go test -json -shuffle=on ./...