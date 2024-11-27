#!/bin/bash

version=v2.0.0-rc.1
phase=0

if [ $phase -eq 0 ]; then
    # Tag the main repo
    pwd
    git tag $version
    git push --tags
fi

if [ $phase -eq 1 ]; then
    # Tag main contribs
    cd ./contrib/net/http && pwd
    git tag contrib/net/http/$version
    git push --tags
    cd -

    cd ./contrib/database/sql && pwd
    git tag contrib/database/sql/$version
    git push --tags
    cd -

    cd ./instrumentation/testutils/grpc && pwd
    git tag instrumentation/testutils/grpc/$version
    git push --tags
    cd -

    echo "WARN: Please run go get in the contribs using the main contribs before running the next phase"
fi

if [ $phase -eq 2 ]; then
    # Tag all contribs
    find ./contrib -type f -name go.mod | while read f; do
        contrib=$(dirname $f)
        if [ "$contrib" == "./contrib/net/http" ]; then
            continue
        fi
        if [ "$contrib" == "./contrib/database/sql" ]; then
            continue
        fi
        cd $contrib && pwd
        git tag $(echo $contrib | sed 's#\.\/##')/$version
        git push --tags
        cd -
    done
fi

find . -type f -name go.mod | while read f; do cd $(dirname $f) && go mod tidy &> /dev/null && cd - &> /dev/null; done
