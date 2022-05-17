#!/usr/bin/env bash

#
# This script matches and parses the given version string and exports its results as github outputs.
# If the argument is an empty string, default to the content of the version file.
# Outputs:
#   - current: the version that was found in the string (eg. v1.2.3-rc.4).
#   - current_without_rc_suffix: the current version without the release-candidate suffix if any (eg. v1.2.3).
#   - next_minor: the current version bumped to the next minor version (eg. v1.3.3).
#   - next_patch: the current version bumped to the next patch version (eg. v1.2.4).
#   - next_rc: the current version bumped to the next release candidate version (eg. v1.2.3-rc.5). Note that if the
#     given current version doesn't have this release-candidate suffix, it will result into vX.Y.Z-rc.1.
#

set -e
source .github/workflows/scripts/lib.sh

str="$1"
if [[ -z "$str" ]]; then
    str="$(cat $version_file_path)"
fi
parse_version "$str"

current=${version[version]}
major=${version[major]}
minor=${version[minor]}
patch=${version[patch]}
rc=${version[rc]}

next_minor="v$major.$(( $minor + 1 )).0"
next_patch="v$major.$minor.$(( $patch + 1 ))"
next_rc="v$major.$minor.$patch-rc.$(( $rc + 1 ))"
current_without_rc_suffix="v$major.$minor.$patch"

echo "The current version is $current (without rc suffix: $current_without_rc_suffix)"
echo "The next minor version is $next_minor"
echo "The next patch version is $next_patch"
echo "The next rc version is $next_rc"

echo "::set-output name=current::$current"
echo "::set-output name=current_without_rc_suffix::$current_without_rc_suffix"
echo "::set-output name=next_minor::$next_minor"
echo "::set-output name=next_patch::$next_patch"
echo "::set-output name=next_rc::$next_rc"
