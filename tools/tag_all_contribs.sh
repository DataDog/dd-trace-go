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
    cd -

    cd ./contrib/database/sql && pwd
    cd -
fi

if [ $phase -eq 2 ]; then
    # Tag all contribs
    find ./contrib -type f -name go.mod | while read f; do
        contrib=$(dirname $f)
        cd $contrib && pwd
        git tag $(echo $contrib | sed 's#\.\/##')/v2.0.0-beta.11
        go mod tidy
        git push --tags
        cd -
    done
fi

find . -type f -name go.mod | while read f; do cd $(dirname $f) && pwd && go mod tidy && cd -; done
