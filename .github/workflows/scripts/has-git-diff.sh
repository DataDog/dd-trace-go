#!/usr/bin/env bash

#
# This script properly uses git diff-index order to know if there are local changes to commit.
# The status code is 0 when there are no changes and 1 otherwise.
#

git update-index --refresh
git diff-index --exit-code HEAD --
