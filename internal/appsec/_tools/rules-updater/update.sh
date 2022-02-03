#!/bin/bash

# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2022 Datadog, Inc.
#

# Generates the rule.go file using the recommended rules for the specified tag version
# Usage: ./update.sh <tag>
# Example: ./update.sh 1.2.5
#

set -e

[ $# -ne 1 ] && echo "Usage: $0 \"version\"" >&2 && exit 1

echo "================ Minifying ================"

tmpDir=$(mktemp -d /tmp/rule-update-XXXXXXXXX)
scriptDir=$PWD/$(dirname $0)

trap "rm -rf $tmpDir" EXIT

DOCKER_BUILDKIT=1 docker build -o type=local,dest=$tmpDir --build-arg version=$1 --no-cache $scriptDir
echo "================   Done    ================"
cp $tmpDir/rule.go .
echo "Output written to $PWD/rule.go"
