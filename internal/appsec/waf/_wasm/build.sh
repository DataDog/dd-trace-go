#
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2016 Datadog, Inc.
#

emcc -flto -g0 -Os -I../include -I./third_party/proj_yamlcpp-prefix/src/proj_yamlcpp/include --no-entry -s EXPORTED_FUNCTIONS="_malloc,_free,_ddwaf_encode,_ddwaf_init,_ddwaf_context_init,_my_ddwaf_run,_my_ddwaf_set_logger"  -o libddwaf.wasm _api.cc ./third_party/proj_yamlcpp-prefix/src/proj_yamlcpp/src/*.cpp libddwaf.a