#!/bin/bash

find ./contrib -type f -name go.mod | while read f; do
    contrib=$(dirname $f)
    cd $contrib && pwd
    git tag $(echo $contrib | sed 's#\.\/##')/v2.0.0-beta.11
    go mod tidy
    git push --tags
    cd -
done