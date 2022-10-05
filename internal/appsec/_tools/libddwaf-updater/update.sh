#!/bin/sh

#
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2016 Datadog, Inc.
#

# Update the libddwaf to the latest GitHub release version.
# Usage: ./update-libddwaf.sh
#

set -ex

bindings_dir=$(readlink -f "$(dirname $0)/../../waf")

version=""
if [ $# -eq 1 ]; then
    version=$1
else
    echo Looking up for the latest GitHub release
    latest_release=$(curl -s https://api.github.com/repos/DataDog/libddwaf/releases/latest)
    version=$(jq -r '.tag_name') << EOF
$latest_release
EOF
fi

echo Updating to libddwaf v$version

tmpdir=$(mktemp -d /tmp/libddwaf-XXXXXXXX)
echo Using $tmpdir

LD_REQUIRED_DEFINED="--require-defined=ddwaf_init \
                    --require-defined=ddwaf_get_version \
                    --require-defined=ddwaf_destroy \
                    --require-defined=ddwaf_context_init \
                    --require-defined=ddwaf_context_destroy \
                    --require-defined=ddwaf_required_addresses \
                    --require-defined=ddwaf_result_free \
                    --require-defined=ddwaf_update_rule_data"

run_binutils() {
  docker run -it --rm -v $bindings_dir:$bindings_dir -v $tmpdir:$tmpdir -w $PWD ghcr.io/datadog/binutils-gdb:2.38 $@
}

run_strip() {
  run_binutils $1-strip --strip-dwo --strip-unneeded --strip-debug $2
}

#
# darwin/arm64
#

echo Updating libddwaf for darwin/arm64
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libddwaf-$version-darwin-arm64.tar.gz | tar -xz -C$tmpdir
echo Copying the darwin/arm64 library
cp -v $tmpdir/libddwaf-$version-darwin-arm64/lib/libddwaf.a $bindings_dir/lib/darwin-arm64

#
# darwin/amd64
#

echo Updating libddwaf for darwin/amd64
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libddwaf-$version-darwin-x86_64.tar.gz | tar -xz -C$tmpdir
echo Copying the darwin/amd64 library
cp -v $tmpdir/libddwaf-$version-darwin-x86_64/lib/libddwaf.a $bindings_dir/lib/darwin-amd64

#
# linux/amd64
#

echo Updating libddwaf for linux/amd64
# 1. Download the libddwaf build
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libddwaf-$version-linux-x86_64.tar.gz | tar -xz -C$tmpdir
# 2. Download the libc++ build
libcxx_dir=$tmpdir/libc++-x86_64-linux
mkdir $libcxx_dir
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libc++-static-x86_64-linux.tar.gz | tar -xz -C$libcxx_dir
# 3. Combine libddwaf.a + libc++.a + libc++abi.a + libunwind.a in a single
#  object file by using ld -r
run_binutils x86_64-linux-gnu-ld \
   -r -o $bindings_dir/lib/linux-amd64/libddwaf.a \
   $LD_REQUIRED_DEFINED \
   $tmpdir/libddwaf-$version-linux-x86_64/lib/libddwaf.a $libcxx_dir/libc++.a $libcxx_dir/libc++abi.a $libcxx_dir/libunwind.a
# 4. Strip
run_strip x86_64-linux-gnu $bindings_dir/lib/linux-amd64/libddwaf.a

#
# linux/arm64
#

echo Updating libddwaf for linux/arm64
# 1. Download the libddwaf build
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libddwaf-$version-linux-aarch64.tar.gz | tar -xz -C$tmpdir
# 2. Download the libc++ build
libcxx_dir=$tmpdir/libc++-aarch64-linux
mkdir $libcxx_dir
curl -L https://github.com/DataDog/libddwaf/releases/download/$version/libc++-static-aarch64-linux.tar.gz | tar -xz -C$libcxx_dir
# 3. Combine libddwaf.a + libc++.a + libc++abi.a + libunwind.a in a single
#  object file by using ld -r
run_binutils aarch64-linux-gnu-ld \
   -r -o $bindings_dir/lib/linux-arm64/libddwaf.a \
   $LD_REQUIRED_DEFINED \
   $tmpdir/libddwaf-$version-linux-aarch64/lib/libddwaf.a $libcxx_dir/libc++.a $libcxx_dir/libc++abi.a $libcxx_dir/libunwind.a
# 4. Strip
run_strip aarch64-linux-gnu $bindings_dir/lib/linux-arm64/libddwaf.a

#
# ddwaf.h
# Note that we arbitrarily take it from the linux/amd64 archive as it does not
# depend on the target.
#
echo Updating ddwaf.h
cp -v $tmpdir/libddwaf-$version-linux-x86_64/include/ddwaf.h $bindings_dir/include
