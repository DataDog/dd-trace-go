// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

#ifndef PROFILER_INTERNAL_H
#define PROFILER_INTERNAL_H
#include <stddef.h>

void profile_allocation(size_t size);
void profile_allocation_checked(size_t size, void *ret_addr);
#endif
