#!/usr/bin/env bash

#
# This script renders the release template with the given template variables:
# render-pr-template.sh \
#   <RELEASE_VERSION> \
#   <RELEASE_NOTE_URL> \
#   <NEXT_MINOR_RELEASE_VERSION>
#

set -e

if [[ $# -ne 3 ]]; then
  echo unexpected number of arguments
  exit 1
fi

RELEASE_VERSION="$1"
RELEASE_NOTE_URL="$2"
NEXT_MINOR_RELEASE_VERSION="$3"

sed -e "s#\$RELEASE_VERSION#$RELEASE_VERSION#g" \
    -e "s#\$RELEASE_NOTE_URL#$RELEASE_NOTE_URL#g" \
    -e "s#\$NEXT_MINOR_RELEASE_VERSION#$NEXT_MINOR_RELEASE_VERSION#g" \
    .github/PULL_REQUEST_TEMPLATE/release.md