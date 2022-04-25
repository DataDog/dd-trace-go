load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "io_bazel_rules_go",
    sha256 = "69de5c704a05ff37862f7e0f5534d4f479418afc21806c887db544a316f3cb6b",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_go/releases/download/v0.27.0/rules_go-v0.27.0.tar.gz",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.27.0/rules_go-v0.27.0.tar.gz",
    ],
)

http_archive(
    name = "bazel_gazelle",
    sha256 = "62ca106be173579c0a167deb23358fdfe71ffa1e4cfdddf5582af26520f1c66f",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.23.0/bazel-gazelle-v0.23.0.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.23.0/bazel-gazelle-v0.23.0.tar.gz",
    ],
)

load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")
load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies", "go_repository")

go_repository(
    name = "co_honnef_go_tools",
    importpath = "honnef.co/go/tools",
    sum = "h1:UoveltGrhghAA7ePc+e+QYDHXrBps2PqFZiHkGR/xK8=",
    version = "v0.0.1-2020.1.4",
)

go_repository(
    name = "com_github_andybalholm_brotli",
    importpath = "github.com/andybalholm/brotli",
    sum = "h1:JKnhI/XQ75uFBTiuzXpzFrUriDPiZjlOSzh6wXogP0E=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_armon_circbuf",
    importpath = "github.com/armon/circbuf",
    sum = "h1:QEF07wC0T1rKkctt1RINW/+RMTVmiwxETico2l3gxJA=",
    version = "v0.0.0-20150827004946-bbbad097214e",
)

go_repository(
    name = "com_github_armon_go_metrics",
    importpath = "github.com/armon/go-metrics",
    sum = "h1:B7AQgHi8QSEi4uHu7Sbsga+IJDU+CENgjxoo81vDUqU=",
    version = "v0.3.0",
)

go_repository(
    name = "com_github_armon_go_radix",
    importpath = "github.com/armon/go-radix",
    sum = "h1:BUAU3CGlLvorLI26FmByPp2eC2qla6E1Tw+scpcg/to=",
    version = "v0.0.0-20180808171621-7fddfc383310",
)

go_repository(
    name = "com_github_aws_aws_sdk_go",
    importpath = "github.com/aws/aws-sdk-go",
    sum = "h1:sscPpn/Ns3i0F4HPEWAVcwdIRaZZCuL7llJ2/60yPIk=",
    version = "v1.34.28",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2",
    importpath = "github.com/aws/aws-sdk-go-v2",
    sum = "h1:ncEVPoHArsG+HjoDe/3ex/TG1CbLwMQ4eaWj0UGdyTo=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_config",
    importpath = "github.com/aws/aws-sdk-go-v2/config",
    sum = "h1:x6vSFAwqAvhYPeSu60f0ZUlGHo3PKKmwDOTL8aMXtv4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_credentials",
    importpath = "github.com/aws/aws-sdk-go-v2/credentials",
    sum = "h1:0M7netgZ8gCV4v7z1km+Fbl7j6KQYyZL7SS0/l5Jn/4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_feature_ec2_imds",
    importpath = "github.com/aws/aws-sdk-go-v2/feature/ec2/imds",
    sum = "h1:lO7fH5n7Q1dKcDBpuTmwJylD1bOQiRig8LI6TD9yVQk=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_service_internal_presigned_url",
    importpath = "github.com/aws/aws-sdk-go-v2/service/internal/presigned-url",
    sum = "h1:IAutMPSrynpvKOpHG6HyWHmh1xmxWAmYOK84NrQVqVQ=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_service_sqs",
    importpath = "github.com/aws/aws-sdk-go-v2/service/sqs",
    sum = "h1:k+iXUEMp688JqUcxb4/bzt7xgJX4TLqahrwgWA/qO6E=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_aws_sdk_go_v2_service_sts",
    importpath = "github.com/aws/aws-sdk-go-v2/service/sts",
    sum = "h1:6XCgxNfE4L/Fnq+InhVNd16DKc6Ue1f3dJl3IwwJRUQ=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_aws_smithy_go",
    importpath = "github.com/aws/smithy-go",
    sum = "h1:nOfSDwiiH232f90OuevPnAEQO5ZqH+xnn8uGVsvBCw4=",
    version = "v1.11.0",
)

go_repository(
    name = "com_github_azure_go_autorest_autorest",
    importpath = "github.com/Azure/go-autorest/autorest",
    sum = "h1:MRvx8gncNaXJqOoLmhNjUAKh33JJF8LyxPhomEtOsjs=",
    version = "v0.9.0",
)

go_repository(
    name = "com_github_azure_go_autorest_autorest_adal",
    importpath = "github.com/Azure/go-autorest/autorest/adal",
    sum = "h1:q2gDruN08/guU9vAjuPWff0+QIrpH6ediguzdAzXAUU=",
    version = "v0.5.0",
)

go_repository(
    name = "com_github_azure_go_autorest_autorest_date",
    importpath = "github.com/Azure/go-autorest/autorest/date",
    sum = "h1:YGrhWfrgtFs84+h0o46rJrlmsZtyZRg470CqAXTZaGM=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_azure_go_autorest_autorest_mocks",
    importpath = "github.com/Azure/go-autorest/autorest/mocks",
    sum = "h1:Ww5g4zThfD/6cLb4z6xxgeyDa7QDkizMkJKe0ysZXp0=",
    version = "v0.2.0",
)

go_repository(
    name = "com_github_azure_go_autorest_logger",
    importpath = "github.com/Azure/go-autorest/logger",
    sum = "h1:ruG4BSDXONFRrZZJ2GUXDiUyVpayPmb1GnWeHDdaNKY=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_azure_go_autorest_tracing",
    importpath = "github.com/Azure/go-autorest/tracing",
    sum = "h1:TRn4WjSnkcSy5AEG3pnbtFSwNtwzjr4VYyQflFE619k=",
    version = "v0.5.0",
)

go_repository(
    name = "com_github_beorn7_perks",
    importpath = "github.com/beorn7/perks",
    sum = "h1:xJ4a3vCFaGF/jqvzLMYoU8P317H5OQ+Via4RmuPwCS0=",
    version = "v0.0.0-20180321164747-3a771d992973",
)

go_repository(
    name = "com_github_bgentry_speakeasy",
    importpath = "github.com/bgentry/speakeasy",
    sum = "h1:ByYyxL9InA1OWqxJqqp2A5pYHUrCiAL6K3J+LKSsQkY=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_bitly_go_hostpool",
    importpath = "github.com/bitly/go-hostpool",
    sum = "h1:mXoPYz/Ul5HYEDvkta6I8/rnYM5gSdSV2tJ6XbZuEtY=",
    version = "v0.0.0-20171023180738-a3a6125de932",
)

go_repository(
    name = "com_github_bmizerany_assert",
    importpath = "github.com/bmizerany/assert",
    sum = "h1:DDGfHa7BWjL4YnC6+E63dPcxHo2sUxDIu8g3QgEJdRY=",
    version = "v0.0.0-20160611221934-b7ed37b82869",
)

go_repository(
    name = "com_github_bradfitz_gomemcache",
    importpath = "github.com/bradfitz/gomemcache",
    sum = "h1:pVrfxiGfwelyab6n21ZBkbkmbevaf+WvMIiR7sr97hw=",
    version = "v0.0.0-20220106215444-fb4bf637b56d",
)

go_repository(
    name = "com_github_burntsushi_toml",
    importpath = "github.com/BurntSushi/toml",
    sum = "h1:WXkYYl6Yr3qBf1K79EBnL4mak0OimBfB0XUf9Vl28OQ=",
    version = "v0.3.1",
)

go_repository(
    name = "com_github_burntsushi_xgb",
    importpath = "github.com/BurntSushi/xgb",
    sum = "h1:1BDTz0u9nC3//pOCMdNH+CiXJVYJh5UQNCOBG7jbELc=",
    version = "v0.0.0-20160522181843-27f122750802",
)

go_repository(
    name = "com_github_census_instrumentation_opencensus_proto",
    importpath = "github.com/census-instrumentation/opencensus-proto",
    sum = "h1:glEXhBS5PSLLv4IXzLA5yPRVX4bilULVyxxbrfOtDAk=",
    version = "v0.2.1",
)

go_repository(
    name = "com_github_cespare_xxhash_v2",
    importpath = "github.com/cespare/xxhash/v2",
    sum = "h1:YRXhKfTDauu4ajMg1TPgFO5jnlC2HCbmLXMcTG5cbYE=",
    version = "v2.1.2",
)

go_repository(
    name = "com_github_chzyer_logex",
    importpath = "github.com/chzyer/logex",
    sum = "h1:Swpa1K6QvQznwJRcfTfQJmTE72DqScAa40E+fbHEXEE=",
    version = "v1.1.10",
)

go_repository(
    name = "com_github_chzyer_readline",
    importpath = "github.com/chzyer/readline",
    sum = "h1:fY5BOSpyZCqRo5OhCuC+XN+r/bBCmeuuJtjz+bCNIf8=",
    version = "v0.0.0-20180603132655-2972be24d48e",
)

go_repository(
    name = "com_github_chzyer_test",
    importpath = "github.com/chzyer/test",
    sum = "h1:q763qf9huN11kDQavWsoZXJNW3xEE4JJyHa5Q25/sd8=",
    version = "v0.0.0-20180213035817-a1ea475d72b1",
)

go_repository(
    name = "com_github_circonus_labs_circonus_gometrics",
    importpath = "github.com/circonus-labs/circonus-gometrics",
    sum = "h1:C29Ae4G5GtYyYMm1aztcyj/J5ckgJm2zwdDajFbx1NY=",
    version = "v2.3.1+incompatible",
)

go_repository(
    name = "com_github_circonus_labs_circonusllhist",
    importpath = "github.com/circonus-labs/circonusllhist",
    sum = "h1:TJH+oke8D16535+jHExHj4nQvzlZrj7ug5D7I/orNUA=",
    version = "v0.1.3",
)

go_repository(
    name = "com_github_client9_misspell",
    importpath = "github.com/client9/misspell",
    sum = "h1:ta993UF76GwbvJcIo3Y68y/M3WxlpEHPWIGDkJYwzJI=",
    version = "v0.3.4",
)

go_repository(
    name = "com_github_cncf_udpa_go",
    importpath = "github.com/cncf/udpa/go",
    sum = "h1:WBZRG4aNOuI15bLRrCgN8fCq8E5Xuty6jGbmSNEvSsU=",
    version = "v0.0.0-20191209042840-269d4d468f6f",
)

go_repository(
    name = "com_github_cockroachdb_apd",
    importpath = "github.com/cockroachdb/apd",
    sum = "h1:3LFP3629v+1aKXU5Q37mxmRxX/pIu1nijXydLShEq5I=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_confluentinc_confluent_kafka_go",
    importpath = "github.com/confluentinc/confluent-kafka-go",
    sum = "h1:GCEMecax8zLZsCVn1cea7Y1uR/lRCdCDednpkc0NLsY=",
    version = "v1.4.0",
)

go_repository(
    name = "com_github_coreos_go_systemd",
    importpath = "github.com/coreos/go-systemd",
    sum = "h1:JOrtw2xFKzlg+cbHpyrpLDmnN1HqhBfnX7WDiW7eG2c=",
    version = "v0.0.0-20190719114852-fd7a80b32e1f",
)

go_repository(
    name = "com_github_creack_pty",
    importpath = "github.com/creack/pty",
    sum = "h1:uDmaGzcdjhF4i/plgjmEsriH11Y0o7RKapEf/LDaM3w=",
    version = "v1.1.9",
)

go_repository(
    name = "com_github_datadog_datadog_agent_pkg_obfuscate",
    importpath = "github.com/DataDog/datadog-agent/pkg/obfuscate",
    sum = "h1:3nVO1nQyh64IUY6BPZUpMYMZ738Pu+LsMt3E0eqqIYw=",
    version = "v0.0.0-20211129110424-6491aa3bf583",
)

go_repository(
    name = "com_github_datadog_datadog_go",
    importpath = "github.com/DataDog/datadog-go",
    sum = "h1:qbcKSx29aBLD+5QLvlQZlGmRMF/FfGqFLFev/1TDzRo=",
    version = "v4.8.2+incompatible",
)

go_repository(
    name = "com_github_datadog_datadog_go_v5",
    importpath = "github.com/DataDog/datadog-go/v5",
    sum = "h1:UFtEe7662/Qojxkw1d6SboAeA0CPI3naKhVASwFn+04=",
    version = "v5.0.2",
)

go_repository(
    name = "com_github_datadog_gostackparse",
    importpath = "github.com/DataDog/gostackparse",
    sum = "h1:jb72P6GFHPHz2W0onsN51cS3FkaMDcjb0QzgxxA4gDk=",
    version = "v0.5.0",
)

go_repository(
    name = "com_github_datadog_sketches_go",
    importpath = "github.com/DataDog/sketches-go",
    sum = "h1:chm5KSXO7kO+ywGWJ0Zs6tdmWU8PBXSbywFVciL6BG4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_datadog_zstd",
    importpath = "github.com/DataDog/zstd",
    sum = "h1:DtpNbljikUepEPD16hD4LvIcmhnhdLTiW/5pHgbmp14=",
    version = "v1.3.5",
)

go_repository(
    name = "com_github_davecgh_go_spew",
    importpath = "github.com/davecgh/go-spew",
    sum = "h1:vj9j/u1bqnvCEfJOwUhtlOARqs3+rkHYY13jYWTU97c=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_denisenkom_go_mssqldb",
    importpath = "github.com/denisenkom/go-mssqldb",
    sum = "h1:9rHa233rhdOyrz2GcP9NM+gi2psgJZ4GWDpL/7ND8HI=",
    version = "v0.11.0",
)

go_repository(
    name = "com_github_dgraph_io_ristretto",
    importpath = "github.com/dgraph-io/ristretto",
    sum = "h1:Jv3CGQHp9OjuMBSne1485aDpUkTKEcUqF+jm/LuerPI=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_dgrijalva_jwt_go",
    importpath = "github.com/dgrijalva/jwt-go",
    sum = "h1:7qlOGliEKZXTDg6OTjfoBKDXWrumCAMpl/TFQ4/5kLM=",
    version = "v3.2.0+incompatible",
)

go_repository(
    name = "com_github_dgryski_go_farm",
    importpath = "github.com/dgryski/go-farm",
    sum = "h1:tdlZCpZ/P9DhczCTSixgIKmwPv6+wP5DGjqLYw5SUiA=",
    version = "v0.0.0-20190423205320-6a90982ecee2",
)

go_repository(
    name = "com_github_dgryski_go_rendezvous",
    importpath = "github.com/dgryski/go-rendezvous",
    sum = "h1:lO4WD4F/rVNCu3HqELle0jiPLLBs70cWOduZpkS1E78=",
    version = "v0.0.0-20200823014737-9f7001d12a5f",
)

go_repository(
    name = "com_github_docker_spdystream",
    importpath = "github.com/docker/spdystream",
    sum = "h1:cenwrSVm+Z7QLSV/BsnenAOcDXdX4cMv4wP0B/5QbPg=",
    version = "v0.0.0-20160310174837-449fdfce4d96",
)

go_repository(
    name = "com_github_dustin_go_humanize",
    importpath = "github.com/dustin/go-humanize",
    sum = "h1:VSnTsYCnlFHaM2/igO1h6X3HA71jcobQuxemgkq4zYo=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_eapache_go_resiliency",
    importpath = "github.com/eapache/go-resiliency",
    sum = "h1:1NtRmCAqadE2FN4ZcN6g90TP3uk8cg9rn9eNK2197aU=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_eapache_go_xerial_snappy",
    importpath = "github.com/eapache/go-xerial-snappy",
    sum = "h1:YEetp8/yCZMuEPMUDHG0CW/brkkEp8mzqk2+ODEitlw=",
    version = "v0.0.0-20180814174437-776d5712da21",
)

go_repository(
    name = "com_github_eapache_queue",
    importpath = "github.com/eapache/queue",
    sum = "h1:YOEu7KNc61ntiQlcEeUIoDTJ2o8mQznoNvUhiigpIqc=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_elastic_go_elasticsearch_v6",
    importpath = "github.com/elastic/go-elasticsearch/v6",
    sum = "h1:U2HtkBseC1FNBmDr0TR2tKltL6FxoY+niDAlj5M8TK8=",
    version = "v6.8.5",
)

go_repository(
    name = "com_github_elastic_go_elasticsearch_v7",
    importpath = "github.com/elastic/go-elasticsearch/v7",
    sum = "h1:49mHcHx7lpCL8cW1aioEwSEVKQF3s+Igi4Ye/QTWwmk=",
    version = "v7.17.1",
)

go_repository(
    name = "com_github_elazarl_goproxy",
    importpath = "github.com/elazarl/goproxy",
    sum = "h1:p1yVGRW3nmb85p1Sh1ZJSDm4A4iKLS5QNbvUHMgGu/M=",
    version = "v0.0.0-20170405201442-c4fc26588b6e",
)

go_repository(
    name = "com_github_emicklei_go_restful",
    importpath = "github.com/emicklei/go-restful",
    sum = "h1:H2pdYOb3KQ1/YsqVWoWNLQO+fusocsw354rqGTZtAgw=",
    version = "v0.0.0-20170410110728-ff4f55a20633",
)

go_repository(
    name = "com_github_envoyproxy_go_control_plane",
    importpath = "github.com/envoyproxy/go-control-plane",
    sum = "h1:rEvIZUSZ3fx39WIi3JkQqQBitGwpELBIYWeBVh6wn+E=",
    version = "v0.9.4",
)

go_repository(
    name = "com_github_envoyproxy_protoc_gen_validate",
    importpath = "github.com/envoyproxy/protoc-gen-validate",
    sum = "h1:EQciDnbrYxy13PgWoY8AqoxGiPrpgBZ1R8UNe3ddc+A=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_erikstmartin_go_testdb",
    importpath = "github.com/erikstmartin/go-testdb",
    sum = "h1:Yzb9+7DPaBjB8zlTR87/ElzFsnQfuHnVUVqpZZIcV5Y=",
    version = "v0.0.0-20160219214506-8d10e4a1bae5",
)

go_repository(
    name = "com_github_evanphx_json_patch",
    importpath = "github.com/evanphx/json-patch",
    sum = "h1:fUDGZCv/7iAN7u0puUVhvKCcsR6vRfwrJatElLBEf0I=",
    version = "v4.2.0+incompatible",
)

go_repository(
    name = "com_github_fatih_color",
    importpath = "github.com/fatih/color",
    sum = "h1:8xPHl4/q1VyqGIPif1F+1V3Y3lSmrq01EabUW3CoW5s=",
    version = "v1.9.0",
)

go_repository(
    name = "com_github_fatih_structs",
    importpath = "github.com/fatih/structs",
    sum = "h1:Q7juDM0QtcnhCpeyLGQKyg4TOIghuNXrkL32pHAUMxo=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_fortytw2_leaktest",
    importpath = "github.com/fortytw2/leaktest",
    sum = "h1:u8491cBMTQ8ft8aeV+adlcytMZylmA5nnwwkRZjI8vw=",
    version = "v1.3.0",
)

go_repository(
    name = "com_github_frankban_quicktest",
    importpath = "github.com/frankban/quicktest",
    sum = "h1:yNZif1OkDfNoDfb9zZa9aXIpejNR4F23Wely0c+Qdqk=",
    version = "v1.13.0",
)

go_repository(
    name = "com_github_fsnotify_fsnotify",
    importpath = "github.com/fsnotify/fsnotify",
    sum = "h1:hsms1Qyu0jgnwNXIxa+/V/PDsU6CfLf6CNO8H7IWoS4=",
    version = "v1.4.9",
)

go_repository(
    name = "com_github_garyburd_redigo",
    importpath = "github.com/garyburd/redigo",
    sum = "h1:HCeeRluvAgMusMomi1+6Y5dmFOdYV/JzoRrrbFlkGIc=",
    version = "v1.6.3",
)

go_repository(
    name = "com_github_ghodss_yaml",
    importpath = "github.com/ghodss/yaml",
    sum = "h1:ZktWZesgun21uEDrwW7iEV1zPCGQldM2atlJZ3TdvVM=",
    version = "v0.0.0-20150909031657-73d445a93680",
)

go_repository(
    name = "com_github_gin_contrib_sse",
    importpath = "github.com/gin-contrib/sse",
    sum = "h1:Y/yl/+YNO8GZSjAhjMsSuLt29uWRFHdHYUb5lYOV9qE=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_gin_gonic_gin",
    importpath = "github.com/gin-gonic/gin",
    sum = "h1:jGB9xAJQ12AIGNB4HguylppmDK1Am9ppF7XnGXXJuoU=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_globalsign_mgo",
    importpath = "github.com/globalsign/mgo",
    sum = "h1:DujepqpGd1hyOd7aW59XpK7Qymp8iy83xq74fLr21is=",
    version = "v0.0.0-20181015135952-eeefdecb41b8",
)

go_repository(
    name = "com_github_go_asn1_ber_asn1_ber",
    importpath = "github.com/go-asn1-ber/asn1-ber",
    sum = "h1:gvPdv/Hr++TRFCl0UbPFHC54P9N9jgsRPnmnr419Uck=",
    version = "v1.3.1",
)

go_repository(
    name = "com_github_go_chi_chi",
    importpath = "github.com/go-chi/chi",
    sum = "h1:2ZcJZozJ+rj6BA0c19ffBUGXEKAT/aOLOtQjD46vBRA=",
    version = "v1.5.0",
)

go_repository(
    name = "com_github_go_chi_chi_v5",
    importpath = "github.com/go-chi/chi/v5",
    sum = "h1:DBPx88FjZJH3FsICfDAfIfnb7XxKIYVGG6lOPlhENAg=",
    version = "v5.0.0",
)

go_repository(
    name = "com_github_go_gl_glfw",
    importpath = "github.com/go-gl/glfw",
    sum = "h1:QbL/5oDUmRBzO9/Z7Seo6zf912W/a6Sr4Eu0G/3Jho0=",
    version = "v0.0.0-20190409004039-e6da0acd62b1",
)

go_repository(
    name = "com_github_go_gl_glfw_v3_3_glfw",
    importpath = "github.com/go-gl/glfw/v3.3/glfw",
    sum = "h1:WtGNWLvXpe6ZudgnXrq0barxBImvnnJoMEhXAzcbM0I=",
    version = "v0.0.0-20200222043503-6f7a984d4dc4",
)

go_repository(
    name = "com_github_go_kit_log",
    importpath = "github.com/go-kit/log",
    sum = "h1:DGJh0Sm43HbOeYDNnVZFl8BvcYVvjD5bqYJvp0REbwQ=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_go_ldap_ldap_v3",
    importpath = "github.com/go-ldap/ldap/v3",
    sum = "h1:RIgdpHXJpsUqUK5WXwKyVsESrGFqo5BRWPk3RR4/ogQ=",
    version = "v3.1.3",
)

go_repository(
    name = "com_github_go_logfmt_logfmt",
    importpath = "github.com/go-logfmt/logfmt",
    sum = "h1:TrB8swr/68K7m9CcGut2g3UOihhbcbiMAYiuTXdEih4=",
    version = "v0.5.0",
)

go_repository(
    name = "com_github_go_logr_logr",
    importpath = "github.com/go-logr/logr",
    sum = "h1:M1Tv3VzNlEHg6uyACnRdtrploV2P7wZqH8BoQMtz0cg=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_go_openapi_jsonpointer",
    importpath = "github.com/go-openapi/jsonpointer",
    sum = "h1:wSt/4CYxs70xbATrGXhokKF1i0tZjENLOo1ioIO13zk=",
    version = "v0.0.0-20160704185906-46af16f9f7b1",
)

go_repository(
    name = "com_github_go_openapi_jsonreference",
    importpath = "github.com/go-openapi/jsonreference",
    sum = "h1:tF+augKRWlWx0J0B7ZyyKSiTyV6E1zZe+7b3qQlcEf8=",
    version = "v0.0.0-20160704190145-13c6e3589ad9",
)

go_repository(
    name = "com_github_go_openapi_spec",
    importpath = "github.com/go-openapi/spec",
    sum = "h1:C1JKChikHGpXwT5UQDFaryIpDtyyGL/CR6C2kB7F1oc=",
    version = "v0.0.0-20160808142527-6aced65f8501",
)

go_repository(
    name = "com_github_go_openapi_swag",
    importpath = "github.com/go-openapi/swag",
    sum = "h1:zP3nY8Tk2E6RTkqGYrarZXuzh+ffyLDljLxCy1iJw80=",
    version = "v0.0.0-20160704191624-1d0bd113de87",
)

go_repository(
    name = "com_github_go_pg_pg_v10",
    importpath = "github.com/go-pg/pg/v10",
    sum = "h1:2XP/r9XdRfiC+LKWrIwqi2qqc+bhvW7/UpUVnwkT7wk=",
    version = "v10.0.0",
)

go_repository(
    name = "com_github_go_pg_zerochecker",
    importpath = "github.com/go-pg/zerochecker",
    sum = "h1:pp7f72c3DobMWOb2ErtZsnrPaSvHd2W4o9//8HtF4mU=",
    version = "v0.2.0",
)

go_repository(
    name = "com_github_go_playground_assert_v2",
    importpath = "github.com/go-playground/assert/v2",
    sum = "h1:MsBgLAaY856+nPRTKrp3/OZK38U/wa0CcBYNjji3q3A=",
    version = "v2.0.1",
)

go_repository(
    name = "com_github_go_playground_locales",
    importpath = "github.com/go-playground/locales",
    sum = "h1:HyWk6mgj5qFqCT5fjGBuRArbVDfE4hi8+e8ceBS/t7Q=",
    version = "v0.13.0",
)

go_repository(
    name = "com_github_go_playground_universal_translator",
    importpath = "github.com/go-playground/universal-translator",
    sum = "h1:icxd5fm+REJzpZx7ZfpaD876Lmtgy7VtROAbHHXk8no=",
    version = "v0.17.0",
)

go_repository(
    name = "com_github_go_playground_validator_v10",
    importpath = "github.com/go-playground/validator/v10",
    sum = "h1:pH2c5ADXtd66mxoE0Zm9SUhxE20r7aM3F26W0hOn+GE=",
    version = "v10.4.1",
)

go_repository(
    name = "com_github_go_redis_redis",
    importpath = "github.com/go-redis/redis",
    sum = "h1:K0pv1D7EQUjfyoMql+r/jZqCLizCGKFlFgcHWWmHQjg=",
    version = "v6.15.9+incompatible",
)

go_repository(
    name = "com_github_go_redis_redis_v7",
    importpath = "github.com/go-redis/redis/v7",
    sum = "h1:I4C4a8UGbFejiVjtYVTRVOiMIJ5pm5Yru6ibvDX/OS0=",
    version = "v7.1.0",
)

go_repository(
    name = "com_github_go_redis_redis_v8",
    importpath = "github.com/go-redis/redis/v8",
    sum = "h1:PC0VsF9sFFd2sko5bu30aEFc8F1TKl6n65o0b8FnCIE=",
    version = "v8.0.0",
)

go_repository(
    name = "com_github_go_sql_driver_mysql",
    importpath = "github.com/go-sql-driver/mysql",
    sum = "h1:BCTh4TKNUYmOmMUcQ3IipzF5prigylS7XXjEkfCHuOE=",
    version = "v1.6.0",
)

go_repository(
    name = "com_github_go_stack_stack",
    importpath = "github.com/go-stack/stack",
    sum = "h1:5SgMzNM5HxrEjV0ww2lTmX6E2Izsfxas4+YHWRs3Lsk=",
    version = "v1.8.0",
)

go_repository(
    name = "com_github_go_task_slim_sprig",
    importpath = "github.com/go-task/slim-sprig",
    sum = "h1:p104kn46Q8WdvHunIJ9dAyjPVtrBPhSr3KT2yUst43I=",
    version = "v0.0.0-20210107165309-348f09dbbbc0",
)

go_repository(
    name = "com_github_go_test_deep",
    importpath = "github.com/go-test/deep",
    sum = "h1:onZX1rnHT3Wv6cqNgYyFOOlgVKJrksuCMCRvJStbMYw=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_gobuffalo_attrs",
    importpath = "github.com/gobuffalo/attrs",
    sum = "h1:hSkbZ9XSyjyBirMeqSqUrK+9HboWrweVlzRNqoBi2d4=",
    version = "v0.0.0-20190224210810-a9411de4debd",
)

go_repository(
    name = "com_github_gobuffalo_depgen",
    importpath = "github.com/gobuffalo/depgen",
    sum = "h1:31atYa/UW9V5q8vMJ+W6wd64OaaTHUrCUXER358zLM4=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_gobuffalo_envy",
    importpath = "github.com/gobuffalo/envy",
    sum = "h1:GlXgaiBkmrYMHco6t4j7SacKO4XUjvh5pwXh0f4uxXU=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_gobuffalo_flect",
    importpath = "github.com/gobuffalo/flect",
    sum = "h1:3GQ53z7E3o00C/yy7Ko8VXqQXoJGLkrTQCLTF1EjoXU=",
    version = "v0.1.3",
)

go_repository(
    name = "com_github_gobuffalo_genny",
    importpath = "github.com/gobuffalo/genny",
    sum = "h1:iQ0D6SpNXIxu52WESsD+KoQ7af2e3nCfnSBoSF/hKe0=",
    version = "v0.1.1",
)

go_repository(
    name = "com_github_gobuffalo_gitgen",
    importpath = "github.com/gobuffalo/gitgen",
    sum = "h1:mSVZ4vj4khv+oThUfS+SQU3UuFIZ5Zo6UNcvK8E8Mz8=",
    version = "v0.0.0-20190315122116-cc086187d211",
)

go_repository(
    name = "com_github_gobuffalo_gogen",
    importpath = "github.com/gobuffalo/gogen",
    sum = "h1:dLg+zb+uOyd/mKeQUYIbwbNmfRsr9hd/WtYWepmayhI=",
    version = "v0.1.1",
)

go_repository(
    name = "com_github_gobuffalo_logger",
    importpath = "github.com/gobuffalo/logger",
    sum = "h1:8thhT+kUJMTMy3HlX4+y9Da+BNJck+p109tqqKp7WDs=",
    version = "v0.0.0-20190315122211-86e12af44bc2",
)

go_repository(
    name = "com_github_gobuffalo_mapi",
    importpath = "github.com/gobuffalo/mapi",
    sum = "h1:fq9WcL1BYrm36SzK6+aAnZ8hcp+SrmnDyAxhNx8dvJk=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_gobuffalo_packd",
    importpath = "github.com/gobuffalo/packd",
    sum = "h1:4sGKOD8yaYJ+dek1FDkwcxCHA40M4kfKgFHx8N2kwbU=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_gobuffalo_packr_v2",
    importpath = "github.com/gobuffalo/packr/v2",
    sum = "h1:Ir9W9XIm9j7bhhkKE9cokvtTl1vBm62A/fene/ZCj6A=",
    version = "v2.2.0",
)

go_repository(
    name = "com_github_gobuffalo_syncx",
    importpath = "github.com/gobuffalo/syncx",
    sum = "h1:tpom+2CJmpzAWj5/VEHync2rJGi+epHNIeRSWjzGA+4=",
    version = "v0.0.0-20190224160051-33c29581e754",
)

go_repository(
    name = "com_github_gocql_gocql",
    importpath = "github.com/gocql/gocql",
    sum = "h1:6ImvI6U901e1ezn/8u2z3bh1DZIvMOia0yTSBxhy4Ao=",
    version = "v0.0.0-20220224095938-0eacd3183625",
)

go_repository(
    name = "com_github_gofiber_fiber_v2",
    importpath = "github.com/gofiber/fiber/v2",
    sum = "h1:97PoVZI3JLlJyfMHFhKZoEHQEfTwOXvhQs2+YoLr9jk=",
    version = "v2.11.0",
)

go_repository(
    name = "com_github_gofrs_uuid",
    importpath = "github.com/gofrs/uuid",
    sum = "h1:1SD/1F5pU8p29ybwgQSwpQk+mwdRrXCYuPhW6m+TnJw=",
    version = "v4.0.0+incompatible",
)

go_repository(
    name = "com_github_gogo_protobuf",
    importpath = "github.com/gogo/protobuf",
    sum = "h1:DqDEcV5aeaTmdFBePNpYsp3FlcVH/2ISVVM9Qf8PSls=",
    version = "v1.3.1",
)

go_repository(
    name = "com_github_golang_glog",
    importpath = "github.com/golang/glog",
    sum = "h1:VKtxabqXZkF25pY9ekfRL6a582T4P37/31XEstQ5p58=",
    version = "v0.0.0-20160126235308-23def4e6c14b",
)

go_repository(
    name = "com_github_golang_groupcache",
    importpath = "github.com/golang/groupcache",
    sum = "h1:1r7pUrabqp18hOBcwBwiTsbnFeTZHV9eER/QT5JVZxY=",
    version = "v0.0.0-20200121045136-8c9f03a8e57e",
)

go_repository(
    name = "com_github_golang_mock",
    importpath = "github.com/golang/mock",
    sum = "h1:GV+pQPG/EUUbkh47niozDcADz6go/dUwhVzdUQHIVRw=",
    version = "v1.4.3",
)

go_repository(
    name = "com_github_golang_protobuf",
    importpath = "github.com/golang/protobuf",
    sum = "h1:ROPKBNFfQgOUMifHyP+KYbvpjbdoFNs+aK7DXlji0Tw=",
    version = "v1.5.2",
)

go_repository(
    name = "com_github_golang_snappy",
    importpath = "github.com/golang/snappy",
    sum = "h1:yAGX7huGHXlcLOEtBnF4w7FQwA26wojNCwOYAEhLjQM=",
    version = "v0.0.4",
)

go_repository(
    name = "com_github_golang_sql_civil",
    importpath = "github.com/golang-sql/civil",
    sum = "h1:lXe2qZdvpiX5WZkZR4hgp4KJVfY3nMkvmwbVkpv1rVY=",
    version = "v0.0.0-20190719163853-cb61b32ac6fe",
)

go_repository(
    name = "com_github_gomodule_redigo",
    importpath = "github.com/gomodule/redigo",
    sum = "h1:ZKld1VOtsGhAe37E7wMxEDgAlGM5dvFY+DiOhSkhP9Y=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_google_btree",
    importpath = "github.com/google/btree",
    sum = "h1:0udJVsspx3VBr5FwtLhQQtuAsVc79tTq0ocGIPAU6qo=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_google_go_cmp",
    importpath = "github.com/google/go-cmp",
    sum = "h1:81/ik6ipDQS2aGcBfIN5dHDB36BwrStyeAQquSYCV4o=",
    version = "v0.5.7",
)

go_repository(
    name = "com_github_google_gofuzz",
    importpath = "github.com/google/gofuzz",
    sum = "h1:xRy4A+RhZaiKjJ1bPfwQ8sedCA+YS2YcCHW6ec7JMi0=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_google_martian",
    importpath = "github.com/google/martian",
    sum = "h1:/CP5g8u/VJHijgedC/Legn3BAbAaWPgecwXBIDzw5no=",
    version = "v2.1.0+incompatible",
)

go_repository(
    name = "com_github_google_pprof",
    importpath = "github.com/google/pprof",
    sum = "h1:l2YRhr+YLzmSp7KJMswRVk/lO5SwoFIcCLzJsVj+YPc=",
    version = "v0.0.0-20210423192551-a2663126120b",
)

go_repository(
    name = "com_github_google_renameio",
    importpath = "github.com/google/renameio",
    sum = "h1:GOZbcHa3HfsPKPlmyPyN2KEohoMXOhdMbHrvbpl2QaA=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_google_uuid",
    importpath = "github.com/google/uuid",
    sum = "h1:t6JiXgmwXMjEs8VusXIJk2BXHsn+wx8BZdTaoZ5fu7I=",
    version = "v1.3.0",
)

go_repository(
    name = "com_github_googleapis_gax_go_v2",
    importpath = "github.com/googleapis/gax-go/v2",
    sum = "h1:sjZBwGj9Jlw33ImPtvFviGYvseOtDM7hkSKB7+Tv3SM=",
    version = "v2.0.5",
)

go_repository(
    name = "com_github_googleapis_gnostic",
    importpath = "github.com/googleapis/gnostic",
    sum = "h1:7XGaL1e6bYS1yIonGp9761ExpPPV1ui0SAC59Yube9k=",
    version = "v0.0.0-20170729233727-0c5108395e2d",
)

go_repository(
    name = "com_github_gophercloud_gophercloud",
    importpath = "github.com/gophercloud/gophercloud",
    sum = "h1:P/nh25+rzXouhytV2pUHBb65fnds26Ghl8/391+sT5o=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_gorilla_context",
    importpath = "github.com/gorilla/context",
    sum = "h1:AWwleXJkX/nhcU9bZSnZoi3h/qGYqQAGhq6zZe/aQW8=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_gorilla_mux",
    importpath = "github.com/gorilla/mux",
    sum = "h1:mq8bRov+5x+pZNR/uAHyUEgovR9gLgYFwDQIeuYi9TM=",
    version = "v1.5.0",
)

go_repository(
    name = "com_github_graph_gophers_graphql_go",
    importpath = "github.com/graph-gophers/graphql-go",
    sum = "h1:Eb9x/q6MFpCLz7jBCiP/WTxjSDrYLR1QY41SORZyNJ0=",
    version = "v1.3.0",
)

go_repository(
    name = "com_github_gregjones_httpcache",
    importpath = "github.com/gregjones/httpcache",
    sum = "h1:pdN6V1QBWetyv/0+wjACpqVH+eVULgEjkurDLq3goeM=",
    version = "v0.0.0-20180305231024-9cad4c3443a7",
)

go_repository(
    name = "com_github_hailocab_go_hostpool",
    importpath = "github.com/hailocab/go-hostpool",
    sum = "h1:5upAirOpQc1Q53c0bnx2ufif5kANL7bfZWcc6VJWJd8=",
    version = "v0.0.0-20160125115350-e80d13ce29ed",
)

go_repository(
    name = "com_github_hashicorp_consul_api",
    importpath = "github.com/hashicorp/consul/api",
    sum = "h1:9GEZpB5zZ2Vm+rQ0t0JL/Ey2iaXbmIN1evA/LUU4do4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_hashicorp_consul_internal",
    importpath = "github.com/hashicorp/consul/internal",
    sum = "h1:jNHZgWe9hbMO05/LRoJYHp/wWNiBwz+Ce6YlppiAEo8=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_hashicorp_errwrap",
    importpath = "github.com/hashicorp/errwrap",
    sum = "h1:OxrOeh75EUXMY8TBjag2fzXGZ40LB6IKw45YeGUDY2I=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_hashicorp_go_cleanhttp",
    importpath = "github.com/hashicorp/go-cleanhttp",
    sum = "h1:035FKYIWjmULyFRBKPs8TBQoi0x6d9G4xc9neXJWAZQ=",
    version = "v0.5.2",
)

go_repository(
    name = "com_github_hashicorp_go_hclog",
    importpath = "github.com/hashicorp/go-hclog",
    sum = "h1:K4ev2ib4LdQETX5cSZBG0DVLk1jwGqSPXBjdah3veNs=",
    version = "v0.16.2",
)

go_repository(
    name = "com_github_hashicorp_go_immutable_radix",
    importpath = "github.com/hashicorp/go-immutable-radix",
    sum = "h1:DKHmCUm2hRBK510BaiZlwvpD40f8bJFeZnpfm2KLowc=",
    version = "v1.3.1",
)

go_repository(
    name = "com_github_hashicorp_go_kms_wrapping_entropy",
    importpath = "github.com/hashicorp/go-kms-wrapping/entropy",
    sum = "h1:xuTi5ZwjimfpvpL09jDE71smCBRpnF5xfo871BSX4gs=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_hashicorp_go_msgpack",
    importpath = "github.com/hashicorp/go-msgpack",
    sum = "h1:zKjpN5BK/P5lMYrLmBHdBULWbJ0XpYR+7NGzqkZzoD4=",
    version = "v0.5.3",
)

go_repository(
    name = "com_github_hashicorp_go_multierror",
    importpath = "github.com/hashicorp/go-multierror",
    sum = "h1:H5DkEtf6CXdFp0N0Em5UCwQpXMWke8IA0+lD48awMYo=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_hashicorp_go_net",
    importpath = "github.com/hashicorp/go.net",
    sum = "h1:sNCoNyDEvN1xa+X0baata4RdcpKwcMS6DH+xwfqPgjw=",
    version = "v0.0.1",
)

go_repository(
    name = "com_github_hashicorp_go_plugin",
    importpath = "github.com/hashicorp/go-plugin",
    sum = "h1:4OtAfUGbnKC6yS48p0CtMX2oFYtzFZVv6rok3cRWgnE=",
    version = "v1.0.1",
)

go_repository(
    name = "com_github_hashicorp_go_retryablehttp",
    importpath = "github.com/hashicorp/go-retryablehttp",
    sum = "h1:HJunrbHTDDbBb/ay4kxa1n+dLmttUlnP3V9oNE4hmsM=",
    version = "v0.6.6",
)

go_repository(
    name = "com_github_hashicorp_go_rootcerts",
    importpath = "github.com/hashicorp/go-rootcerts",
    sum = "h1:jzhAVGtqPKbwpyCPELlgNWhE1znq+qwJtW5Oi2viEzc=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_hashicorp_go_sockaddr",
    importpath = "github.com/hashicorp/go-sockaddr",
    sum = "h1:ztczhD1jLxIRjVejw8gFomI1BQZOe2WoVOu0SyteCQc=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_hashicorp_go_syslog",
    importpath = "github.com/hashicorp/go-syslog",
    sum = "h1:KaodqZuhUoZereWVIYmpUgZysurB1kBLX2j0MwMrUAE=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_hashicorp_go_uuid",
    importpath = "github.com/hashicorp/go-uuid",
    sum = "h1:cfejS+Tpcp13yd5nYHWDI6qVCny6wyX2Mt5SGur2IGE=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_hashicorp_go_version",
    importpath = "github.com/hashicorp/go-version",
    sum = "h1:bPIoEKD27tNdebFGGxxYwcL4nepeY4j1QP23PFRGzg0=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_hashicorp_golang_lru",
    importpath = "github.com/hashicorp/golang-lru",
    sum = "h1:YDjusn29QI/Das2iO9M0BHnIbxPeyuCHsjMW+lJfyTc=",
    version = "v0.5.4",
)

go_repository(
    name = "com_github_hashicorp_hcl",
    importpath = "github.com/hashicorp/hcl",
    sum = "h1:0Anlzjpi4vEasTeNFn2mLJgTSwt0+6sfsiTG8qcWGx4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_hashicorp_logutils",
    importpath = "github.com/hashicorp/logutils",
    sum = "h1:dLEQVugN8vlakKOUE3ihGLTZJRB4j+M2cdTm/ORI65Y=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_hashicorp_mdns",
    importpath = "github.com/hashicorp/mdns",
    sum = "h1:WhIgCr5a7AaVH6jPUwjtRuuE7/RDufnUvzIr48smyxs=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_hashicorp_memberlist",
    importpath = "github.com/hashicorp/memberlist",
    sum = "h1:ouPxvwKYaNZe+eTcHxYP0EblPduVLvIPycul+vv8his=",
    version = "v0.1.6",
)

go_repository(
    name = "com_github_hashicorp_serf",
    importpath = "github.com/hashicorp/serf",
    sum = "h1:w2ZEHuK1297elT/WbZjUojVzpZA3BuPUusa9vdXXTjc=",
    version = "v0.8.6",
)

go_repository(
    name = "com_github_hashicorp_vault_api",
    importpath = "github.com/hashicorp/vault/api",
    sum = "h1:QcxC7FuqEl0sZaIjcXB/kNEeBa0DH5z57qbWBvZwLC4=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_hashicorp_vault_sdk",
    importpath = "github.com/hashicorp/vault/sdk",
    sum = "h1:e1ok06zGrWJW91rzRroyl5nRNqraaBe4d5hiKcVZuHM=",
    version = "v0.1.14-0.20200519221838-e0cfd64bc267",
)

go_repository(
    name = "com_github_hashicorp_yamux",
    importpath = "github.com/hashicorp/yamux",
    sum = "h1:b5rjCoWHc7eqmAS4/qyk21ZsHyb6Mxv/jykxvNTkU4M=",
    version = "v0.0.0-20180604194846-3520598351bb",
)

go_repository(
    name = "com_github_hpcloud_tail",
    importpath = "github.com/hpcloud/tail",
    sum = "h1:nfCOvKYfkgYP8hkirhJocXT2+zOD8yUNjXaWfTlyFKI=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_ianlancetaylor_demangle",
    importpath = "github.com/ianlancetaylor/demangle",
    sum = "h1:mV02weKRL81bEnm8A0HT1/CAelMQDBuQIfLw8n+d6xI=",
    version = "v0.0.0-20200824232613-28f6c0f3b639",
)

go_repository(
    name = "com_github_imdario_mergo",
    importpath = "github.com/imdario/mergo",
    sum = "h1:JboBksRwiiAJWvIYJVo46AfV+IAIKZpfrSzVKj42R4Q=",
    version = "v0.3.5",
)

go_repository(
    name = "com_github_inconshreveable_mousetrap",
    importpath = "github.com/inconshreveable/mousetrap",
    sum = "h1:Z8tu5sraLXCXIcARxBp/8cbvlwVa7Z1NHg9XEKhtSvM=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_jackc_chunkreader",
    importpath = "github.com/jackc/chunkreader",
    sum = "h1:4s39bBR8ByfqH+DKm8rQA3E1LHZWB9XWcrz8fqaZbe0=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_jackc_chunkreader_v2",
    importpath = "github.com/jackc/chunkreader/v2",
    sum = "h1:i+RDz65UE+mmpjTfyz0MoVTnzeYxroil2G82ki7MGG8=",
    version = "v2.0.1",
)

go_repository(
    name = "com_github_jackc_pgconn",
    importpath = "github.com/jackc/pgconn",
    sum = "h1:DzdIHIjG1AxGwoEEqS+mGsURyjt4enSmqzACXvVzOT8=",
    version = "v1.10.1",
)

go_repository(
    name = "com_github_jackc_pgio",
    importpath = "github.com/jackc/pgio",
    sum = "h1:g12B9UwVnzGhueNavwioyEEpAmqMe1E/BN9ES+8ovkE=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_jackc_pgmock",
    importpath = "github.com/jackc/pgmock",
    sum = "h1:DadwsjnMwFjfWc9y5Wi/+Zz7xoE5ALHsRQlOctkOiHc=",
    version = "v0.0.0-20210724152146-4ad1a8207f65",
)

go_repository(
    name = "com_github_jackc_pgpassfile",
    importpath = "github.com/jackc/pgpassfile",
    sum = "h1:/6Hmqy13Ss2zCq62VdNG8tM1wchn8zjSGOBJ6icpsIM=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_jackc_pgproto3",
    importpath = "github.com/jackc/pgproto3",
    sum = "h1:FYYE4yRw+AgI8wXIinMlNjBbp/UitDJwfj5LqqewP1A=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_jackc_pgproto3_v2",
    importpath = "github.com/jackc/pgproto3/v2",
    sum = "h1:r7JypeP2D3onoQTCxWdTpCtJ4D+qpKr0TxvoyMhZ5ns=",
    version = "v2.2.0",
)

go_repository(
    name = "com_github_jackc_pgservicefile",
    importpath = "github.com/jackc/pgservicefile",
    sum = "h1:C8S2+VttkHFdOOCXJe+YGfa4vHYwlt4Zx+IVXQ97jYg=",
    version = "v0.0.0-20200714003250-2b9c44734f2b",
)

go_repository(
    name = "com_github_jackc_pgtype",
    importpath = "github.com/jackc/pgtype",
    sum = "h1:/SH1RxEtltvJgsDqp3TbiTFApD3mey3iygpuEGeuBXk=",
    version = "v1.9.0",
)

go_repository(
    name = "com_github_jackc_pgx_v4",
    importpath = "github.com/jackc/pgx/v4",
    sum = "h1:TgdrmgnM7VY72EuSQzBbBd4JA1RLqJolrw9nQVZABVc=",
    version = "v4.14.0",
)

go_repository(
    name = "com_github_jackc_puddle",
    importpath = "github.com/jackc/puddle",
    sum = "h1:DNDKdn/pDrWvDWyT2FYvpZVE81OAhWrjCv19I9n108Q=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_jinzhu_gorm",
    importpath = "github.com/jinzhu/gorm",
    sum = "h1:lDSDtsCt5AGGSKTs8AHlSDbbgif4G4+CKJ8ETBDVHTA=",
    version = "v1.9.1",
)

go_repository(
    name = "com_github_jinzhu_inflection",
    importpath = "github.com/jinzhu/inflection",
    sum = "h1:K317FqzuhWc8YvSVlFMCCUb36O/S9MCKRDI7QkRKD/E=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_jinzhu_now",
    importpath = "github.com/jinzhu/now",
    sum = "h1:PlHq1bSCSZL9K0wUhbm2pGLoTWs2GwVhsP6emvGV/ZI=",
    version = "v1.1.3",
)

go_repository(
    name = "com_github_jmespath_go_jmespath",
    importpath = "github.com/jmespath/go-jmespath",
    sum = "h1:BEgLn5cpjn8UN1mAw4NjwDrS35OdebyEtFe+9YPoQUg=",
    version = "v0.4.0",
)

go_repository(
    name = "com_github_jmespath_go_jmespath_internal_testify",
    importpath = "github.com/jmespath/go-jmespath/internal/testify",
    sum = "h1:shLQSRRSCCPj3f2gpwzGwWFoC7ycTf1rcQZHOlsJ6N8=",
    version = "v1.5.1",
)

go_repository(
    name = "com_github_jmoiron_sqlx",
    importpath = "github.com/jmoiron/sqlx",
    sum = "h1:41Ip0zITnmWNR/vHV+S4m+VoUivnWY5E4OJfLZjCJMA=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_joho_godotenv",
    importpath = "github.com/joho/godotenv",
    sum = "h1:Zjp+RcGpHhGlrMbJzXTrZZPrWj+1vfm90La1wgB6Bhc=",
    version = "v1.3.0",
)

go_repository(
    name = "com_github_josharian_intern",
    importpath = "github.com/josharian/intern",
    sum = "h1:vlS4z54oSdjm0bgjRigI+G1HpF+tI+9rE5LLzOg8HmY=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_json_iterator_go",
    importpath = "github.com/json-iterator/go",
    sum = "h1:9yzud/Ht36ygwatGx56VwCZtlI/2AD15T1X2sjSuGns=",
    version = "v1.1.9",
)

go_repository(
    name = "com_github_jstemmer_go_junit_report",
    importpath = "github.com/jstemmer/go-junit-report",
    sum = "h1:6QPYqodiu3GuPL+7mfx+NwDdp2eTkp9IfEUpgAwUN0o=",
    version = "v0.9.1",
)

go_repository(
    name = "com_github_julienschmidt_httprouter",
    importpath = "github.com/julienschmidt/httprouter",
    sum = "h1:7wLdtIiIpzOkC9u6sXOozpBauPdskj3ru4EI5MABq68=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_karrick_godirwalk",
    importpath = "github.com/karrick/godirwalk",
    sum = "h1:lOpSw2vJP0y5eLBW906QwKsUK/fe/QDyoqM5rnnuPDY=",
    version = "v1.10.3",
)

go_repository(
    name = "com_github_kisielk_errcheck",
    importpath = "github.com/kisielk/errcheck",
    sum = "h1:reN85Pxc5larApoH1keMBiu2GWtPqXQ1nc9gx+jOU+E=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_kisielk_gotool",
    importpath = "github.com/kisielk/gotool",
    sum = "h1:AV2c/EiW3KqPNT9ZKl07ehoAGi4C5/01Cfbblndcapg=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_klauspost_compress",
    importpath = "github.com/klauspost/compress",
    sum = "h1:S0OHlFk/Gbon/yauFJ4FfJJF5V0fc5HbBTJazi28pRw=",
    version = "v1.14.2",
)

go_repository(
    name = "com_github_konsorten_go_windows_terminal_sequences",
    importpath = "github.com/konsorten/go-windows-terminal-sequences",
    sum = "h1:DB17ag19krx9CFsz4o3enTrPXyIXCl+2iCXH/aMAp9s=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_kr_pretty",
    importpath = "github.com/kr/pretty",
    sum = "h1:Fmg33tUaq4/8ym9TJN1x7sLJnHVwhP33CNkpYV/7rwI=",
    version = "v0.2.1",
)

go_repository(
    name = "com_github_kr_pty",
    importpath = "github.com/kr/pty",
    sum = "h1:AkaSdXYQOWeaO3neb8EM634ahkXXe3jYbVh/F9lq+GI=",
    version = "v1.1.8",
)

go_repository(
    name = "com_github_kr_text",
    importpath = "github.com/kr/text",
    sum = "h1:5Nx0Ya0ZqY2ygV366QzturHI13Jq95ApcVaJBhpS+AY=",
    version = "v0.2.0",
)

go_repository(
    name = "com_github_labstack_echo",
    importpath = "github.com/labstack/echo",
    sum = "h1:pGRcYk231ExFAyoAjAfD85kQzRJCRI8bbnE7CX5OEgg=",
    version = "v3.3.10+incompatible",
)

go_repository(
    name = "com_github_labstack_echo_v4",
    importpath = "github.com/labstack/echo/v4",
    sum = "h1:jkCSsjXmBmapVXF6U4BrSz/cgofWM0CU3Q74wQvXkIc=",
    version = "v4.2.0",
)

go_repository(
    name = "com_github_labstack_gommon",
    importpath = "github.com/labstack/gommon",
    sum = "h1:OomWaJXm7xR6L1HmEtGyQf26TEn7V6X88mktX9kee9o=",
    version = "v0.3.1",
)

go_repository(
    name = "com_github_leodido_go_urn",
    importpath = "github.com/leodido/go-urn",
    sum = "h1:hpXL4XnriNwQ/ABnpepYM/1vCLWNDfUNts8dX3xTG6Y=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_lib_pq",
    importpath = "github.com/lib/pq",
    sum = "h1:AqzbZs4ZoCBp+GtejcpCpcxM3zlSMx29dXbUSeVtJb8=",
    version = "v1.10.2",
)

go_repository(
    name = "com_github_mailru_easyjson",
    importpath = "github.com/mailru/easyjson",
    sum = "h1:UGYAvKxe3sBsEDzO8ZeWOSlIQfWFlxbzLZe7hwFURr0=",
    version = "v0.7.7",
)

go_repository(
    name = "com_github_markbates_oncer",
    importpath = "github.com/markbates/oncer",
    sum = "h1:JgVTCPf0uBVcUSWpyXmGpgOc62nK5HWUBKAGc3Qqa5k=",
    version = "v0.0.0-20181203154359-bf2de49a0be2",
)

go_repository(
    name = "com_github_markbates_safe",
    importpath = "github.com/markbates/safe",
    sum = "h1:yjZkbvRM6IzKj9tlu/zMJLS0n/V351OZWRnF3QfaUxI=",
    version = "v1.0.1",
)

go_repository(
    name = "com_github_masterminds_semver_v3",
    importpath = "github.com/Masterminds/semver/v3",
    sum = "h1:hLg3sBzpNErnxhQtUy/mmLR2I9foDujNK030IGemrRc=",
    version = "v3.1.1",
)

go_repository(
    name = "com_github_mattn_go_colorable",
    importpath = "github.com/mattn/go-colorable",
    sum = "h1:nQ+aFkoE2TMGc0b68U2OKSexC+eq46+XwZzWXHRmPYs=",
    version = "v0.1.11",
)

go_repository(
    name = "com_github_mattn_go_isatty",
    importpath = "github.com/mattn/go-isatty",
    sum = "h1:yVuAays6BHfxijgZPzw+3Zlu5yQgKGP2/hcQbHb7S9Y=",
    version = "v0.0.14",
)

go_repository(
    name = "com_github_mattn_go_sqlite3",
    importpath = "github.com/mattn/go-sqlite3",
    sum = "h1:TJ1bhYJPV44phC+IMu1u2K/i5RriLTPe+yc68XDJ1Z0=",
    version = "v1.14.12",
)

go_repository(
    name = "com_github_matttproud_golang_protobuf_extensions",
    importpath = "github.com/matttproud/golang_protobuf_extensions",
    sum = "h1:4hp9jkHxhMHkqkrB3Ix0jegS5sx/RkqARlsWZ6pIwiU=",
    version = "v1.0.1",
)

go_repository(
    name = "com_github_microsoft_go_winio",
    importpath = "github.com/Microsoft/go-winio",
    sum = "h1:aPJp2QD7OOrhO5tQXqQoGSJc+DjDtWTGLOmNyAm6FgY=",
    version = "v0.5.1",
)

go_repository(
    name = "com_github_miekg_dns",
    importpath = "github.com/miekg/dns",
    sum = "h1:dFwPR6SfLtrSwgDcIq2bcU/gVutB4sNApq2HBdqcakg=",
    version = "v1.1.25",
)

go_repository(
    name = "com_github_mitchellh_cli",
    importpath = "github.com/mitchellh/cli",
    sum = "h1:iGBIsUe3+HZ/AD/Vd7DErOt5sU9fa8Uj7A2s1aggv1Y=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_mitchellh_copystructure",
    importpath = "github.com/mitchellh/copystructure",
    sum = "h1:Laisrj+bAB6b/yJwB5Bt3ITZhGJdqmxquMKeZ+mmkFQ=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_mitchellh_go_homedir",
    importpath = "github.com/mitchellh/go-homedir",
    sum = "h1:lukF9ziXFxDFPkA1vsr5zpc1XuPDn/wFntq5mG+4E0Y=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_mitchellh_go_testing_interface",
    importpath = "github.com/mitchellh/go-testing-interface",
    sum = "h1:fzU/JVNcaqHQEcVFAKeR41fkiLdIPrefOvVG1VZ96U0=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_mitchellh_go_wordwrap",
    importpath = "github.com/mitchellh/go-wordwrap",
    sum = "h1:6GlHJ/LTGMrIJbwgdqdl2eEH8o+Exx/0m8ir9Gns0u4=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_mitchellh_gox",
    importpath = "github.com/mitchellh/gox",
    sum = "h1:lfGJxY7ToLJQjHHwi0EX6uYBdK78egf954SQl13PQJc=",
    version = "v0.4.0",
)

go_repository(
    name = "com_github_mitchellh_iochan",
    importpath = "github.com/mitchellh/iochan",
    sum = "h1:C+X3KsSTLFVBr/tK1eYN/vs4rJcvsiLU338UhYPJWeY=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_mitchellh_mapstructure",
    importpath = "github.com/mitchellh/mapstructure",
    sum = "h1:6h7AQ0yhTcIsmFmnAwQls75jp2Gzs4iB8W7pjMO+rqo=",
    version = "v1.4.2",
)

go_repository(
    name = "com_github_mitchellh_reflectwalk",
    importpath = "github.com/mitchellh/reflectwalk",
    sum = "h1:9D+8oIskB4VJBN5SFlmc27fSlIBZaov1Wpk/IfikLNY=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_modern_go_concurrent",
    importpath = "github.com/modern-go/concurrent",
    sum = "h1:TRLaZ9cD/w8PVh93nsPXa1VrQ6jlwL5oN8l14QlcNfg=",
    version = "v0.0.0-20180306012644-bacd9c7ef1dd",
)

go_repository(
    name = "com_github_modern_go_reflect2",
    importpath = "github.com/modern-go/reflect2",
    sum = "h1:9f412s+6RmYXLWZSEzVVgPGK7C2PphHj5RJrvfx9AWI=",
    version = "v1.0.1",
)

go_repository(
    name = "com_github_montanaflynn_stats",
    importpath = "github.com/montanaflynn/stats",
    sum = "h1:iruDEfMl2E6fbMZ9s0scYfZQ84/6SPL6zC8ACM2oIL0=",
    version = "v0.0.0-20171201202039-1bf9dbcd8cbe",
)

go_repository(
    name = "com_github_munnerz_goautoneg",
    importpath = "github.com/munnerz/goautoneg",
    sum = "h1:7PxY7LVfSZm7PEeBTyK1rj1gABdCO2mbri6GKO1cMDs=",
    version = "v0.0.0-20120707110453-a547fc61f48d",
)

go_repository(
    name = "com_github_mxk_go_flowrate",
    importpath = "github.com/mxk/go-flowrate",
    sum = "h1:y5//uYreIhSUg3J1GEMiLbxo1LJaP8RfCpH6pymGZus=",
    version = "v0.0.0-20140419014527-cca7078d478f",
)

go_repository(
    name = "com_github_niemeyer_pretty",
    importpath = "github.com/niemeyer/pretty",
    sum = "h1:fD57ERR4JtEqsWbfPhv4DMiApHyliiK5xCTNVSPiaAs=",
    version = "v0.0.0-20200227124842-a10e7caefd8e",
)

go_repository(
    name = "com_github_nxadm_tail",
    importpath = "github.com/nxadm/tail",
    sum = "h1:nPr65rt6Y5JFSKQO7qToXr7pePgD6Gwiw05lkbyAQTE=",
    version = "v1.4.8",
)

go_repository(
    name = "com_github_nytimes_gziphandler",
    importpath = "github.com/NYTimes/gziphandler",
    sum = "h1:lsxEuwrXEAokXB9qhlbKWPpo3KMLZQ5WB5WLQRW1uq0=",
    version = "v0.0.0-20170623195520-56545f4a5d46",
)

go_repository(
    name = "com_github_oklog_run",
    importpath = "github.com/oklog/run",
    sum = "h1:Ru7dDtJNOyC66gQ5dQmaCa0qIsAUFY3sFpK1Xk8igrw=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_onsi_ginkgo",
    importpath = "github.com/onsi/ginkgo",
    sum = "h1:29JGrr5oVBm5ulCWet69zQkzWipVXIol6ygQUe/EzNc=",
    version = "v1.16.4",
)

go_repository(
    name = "com_github_onsi_gomega",
    importpath = "github.com/onsi/gomega",
    sum = "h1:6gjqkI8iiRHMvdccRJM8rVKjCWk6ZIm6FTm3ddIe4/c=",
    version = "v1.16.0",
)

go_repository(
    name = "com_github_opentracing_opentracing_go",
    importpath = "github.com/opentracing/opentracing-go",
    sum = "h1:uEJPy/1a5RIPAJ0Ov+OIO8OxWu77jEv+1B0VhjKrZUs=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_pascaldekloe_goe",
    importpath = "github.com/pascaldekloe/goe",
    sum = "h1:cBOtyMzM9HTpWjXfbbunk26uA6nG3a8n06Wieeh0MwY=",
    version = "v0.1.0",
)

go_repository(
    name = "com_github_pelletier_go_toml",
    importpath = "github.com/pelletier/go-toml",
    sum = "h1:7utD74fnzVc/cpcyy8sjrlFr5vYpypUixARcHIMIGuI=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_peterbourgon_diskv",
    importpath = "github.com/peterbourgon/diskv",
    sum = "h1:UBdAOUP5p4RWqPBg048CAvpKN+vxiaj6gdUUzhl4XmI=",
    version = "v2.0.1+incompatible",
)

go_repository(
    name = "com_github_philhofer_fwd",
    importpath = "github.com/philhofer/fwd",
    sum = "h1:GdGcTjf5RNAxwS4QLsiMzJYj5KEvPJD3Abr261yRQXQ=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_pierrec_lz4",
    importpath = "github.com/pierrec/lz4",
    sum = "h1:WCjObylUIOlKy/+7Abdn34TLIkXiA4UWUMhxq9m9ZXI=",
    version = "v2.5.2+incompatible",
)

go_repository(
    name = "com_github_pierrec_lz4_v4",
    importpath = "github.com/pierrec/lz4/v4",
    sum = "h1:+fL8AQEZtz/ijeNnpduH0bROTu0O3NZAlPjQxGn8LwE=",
    version = "v4.1.14",
)

go_repository(
    name = "com_github_pkg_errors",
    importpath = "github.com/pkg/errors",
    sum = "h1:FEBLx1zS214owpjy7qsBeixbURkuhQAwrK5UwLGTwt4=",
    version = "v0.9.1",
)

go_repository(
    name = "com_github_pkg_profile",
    importpath = "github.com/pkg/profile",
    sum = "h1:F++O52m40owAmADcojzM+9gyjmMOY/T4oYJkgFDH8RE=",
    version = "v1.2.1",
)

go_repository(
    name = "com_github_pmezard_go_difflib",
    importpath = "github.com/pmezard/go-difflib",
    sum = "h1:4DBwDE0NGyQoBHbLQYPwSUPoCMWR5BEzIk/f1lZbAQM=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_posener_complete",
    importpath = "github.com/posener/complete",
    sum = "h1:ccV59UEOTzVDnDUEFdT95ZzHVZ+5+158q8+SJb2QV5w=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_prometheus_client_golang",
    importpath = "github.com/prometheus/client_golang",
    sum = "h1:awm861/B8OKDd2I/6o1dy3ra4BamzKhYOiGItCeZ740=",
    version = "v0.9.2",
)

go_repository(
    name = "com_github_prometheus_client_model",
    importpath = "github.com/prometheus/client_model",
    sum = "h1:gQz4mCbXsO+nc9n1hCxHcGA3Zx3Eo+UHZoInFGUIXNM=",
    version = "v0.0.0-20190812154241-14fe0d1b01d4",
)

go_repository(
    name = "com_github_prometheus_common",
    importpath = "github.com/prometheus/common",
    sum = "h1:PnBWHBf+6L0jOqq0gIVUe6Yk0/QMZ640k6NvkxcBf+8=",
    version = "v0.0.0-20181126121408-4724e9255275",
)

go_repository(
    name = "com_github_prometheus_procfs",
    importpath = "github.com/prometheus/procfs",
    sum = "h1:9a8MnZMP0X2nLJdBg+pBmGgkJlSaKC2KaQmTCk1XDtE=",
    version = "v0.0.0-20181204211112-1dc9a6cbc91a",
)

go_repository(
    name = "com_github_puerkitobio_purell",
    importpath = "github.com/PuerkitoBio/purell",
    sum = "h1:0GoNN3taZV6QI81IXgCbxMyEaJDXMSIjArYBCYzVVvs=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_puerkitobio_urlesc",
    importpath = "github.com/PuerkitoBio/urlesc",
    sum = "h1:JCHLVE3B+kJde7bIEo5N4J+ZbLhp0J1Fs+ulyRws4gE=",
    version = "v0.0.0-20160726150825-5bd2802263f2",
)

go_repository(
    name = "com_github_rcrowley_go_metrics",
    importpath = "github.com/rcrowley/go-metrics",
    sum = "h1:9ZKAASQSHhDYGoxY8uLVpewe1GDZ2vu2Tr/vTdVAkFQ=",
    version = "v0.0.0-20181016184325-3113b8401b8a",
)

go_repository(
    name = "com_github_rogpeppe_go_internal",
    importpath = "github.com/rogpeppe/go-internal",
    sum = "h1:RR9dF3JtopPvtkroDZuVD7qquD0bnHlKSqaQhgwt8yk=",
    version = "v1.3.0",
)

go_repository(
    name = "com_github_rs_xid",
    importpath = "github.com/rs/xid",
    sum = "h1:mhH9Nq+C1fY2l1XIpgxIiUOfNpRBYH1kKcr+qfKgjRc=",
    version = "v1.2.1",
)

go_repository(
    name = "com_github_rs_zerolog",
    importpath = "github.com/rs/zerolog",
    sum = "h1:uPRuwkWF4J6fGsJ2R0Gn2jB1EQiav9k3S6CSdygQJXY=",
    version = "v1.15.0",
)

go_repository(
    name = "com_github_ryanuber_columnize",
    importpath = "github.com/ryanuber/columnize",
    sum = "h1:j1Wcmh8OrK4Q7GXY+V7SVSY8nUWQxHW5TkBe7YUl+2s=",
    version = "v2.1.0+incompatible",
)

go_repository(
    name = "com_github_ryanuber_go_glob",
    importpath = "github.com/ryanuber/go-glob",
    sum = "h1:iQh3xXAumdQ+4Ufa5b25cRpC5TYKlno6hsv6Cb3pkBk=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_satori_go_uuid",
    importpath = "github.com/satori/go.uuid",
    sum = "h1:0uYX9dsZ2yD7q2RtLRtPSdGDWzjeM3TbMJP9utgA0ww=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_sean_seed",
    importpath = "github.com/sean-/seed",
    sum = "h1:nn5Wsu0esKSJiIVhscUtVbo7ada43DJhG55ua/hjS5I=",
    version = "v0.0.0-20170313163322-e2103e2c3529",
)

go_repository(
    name = "com_github_segmentio_kafka_go",
    importpath = "github.com/segmentio/kafka-go",
    sum = "h1:4ujULpikzHG0HqKhjumDghFjy/0RRCSl/7lbriwQAH0=",
    version = "v0.4.29",
)

go_repository(
    name = "com_github_shopify_sarama",
    importpath = "github.com/Shopify/sarama",
    sum = "h1:rtiODsvY4jW6nUV6n3K+0gx/8WlAwVt+Ixt6RIvpYyo=",
    version = "v1.22.0",
)

go_repository(
    name = "com_github_shopify_toxiproxy",
    importpath = "github.com/Shopify/toxiproxy",
    sum = "h1:TKdv8HiTLgE5wdJuEML90aBgNWsokNbMijUGhmcoBJc=",
    version = "v2.1.4+incompatible",
)

go_repository(
    name = "com_github_shopspring_decimal",
    importpath = "github.com/shopspring/decimal",
    sum = "h1:abSATXmQEYyShuxI4/vyW3tV1MrKAJzCZ/0zLUXYbsQ=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_sirupsen_logrus",
    importpath = "github.com/sirupsen/logrus",
    sum = "h1:ShrD1U9pZB12TX0cVy0DtePoCH97K8EtX+mg7ZARUtM=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_smartystreets_go_aws_auth",
    importpath = "github.com/smartystreets/go-aws-auth",
    sum = "h1:hp2CYQUINdZMHdvTdXtPOY2ainKl4IoMcpAXEf2xj3Q=",
    version = "v0.0.0-20180515143844-0c1422d1fdb9",
)

go_repository(
    name = "com_github_spf13_afero",
    importpath = "github.com/spf13/afero",
    sum = "h1:5jhuqJyZCZf2JRofRvN/nIFgIWNzPa3/Vz8mYylgbWc=",
    version = "v1.2.2",
)

go_repository(
    name = "com_github_spf13_cobra",
    importpath = "github.com/spf13/cobra",
    sum = "h1:ZlrZ4XsMRm04Fr5pSFxBgfND2EBVa1nLpiy1stUsX/8=",
    version = "v0.0.3",
)

go_repository(
    name = "com_github_spf13_pflag",
    importpath = "github.com/spf13/pflag",
    sum = "h1:iy+VFUOCP1a+8yFto/drg2CJ5u0yRoB7fZw3DKv/JXA=",
    version = "v1.0.5",
)

go_repository(
    name = "com_github_stretchr_objx",
    importpath = "github.com/stretchr/objx",
    sum = "h1:Hbg2NidpLE8veEBkEZTL3CvlkUIVzuU9jDplZO54c48=",
    version = "v0.2.0",
)

go_repository(
    name = "com_github_stretchr_testify",
    importpath = "github.com/stretchr/testify",
    sum = "h1:nwc3DEeHmmLAfoZucVR881uASk0Mfjw8xYJ99tb5CcY=",
    version = "v1.7.0",
)

go_repository(
    name = "com_github_syndtr_goleveldb",
    importpath = "github.com/syndtr/goleveldb",
    sum = "h1:fBdIW9lB4Iz0n9khmH8w27SJ3QEJ7+IgjPEwGSZiFdE=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_tidwall_btree",
    importpath = "github.com/tidwall/btree",
    sum = "h1:5P+9WU8ui5uhmcg3SoPyTwoI0mVyZ1nps7YQzTZFkYM=",
    version = "v1.1.0",
)

go_repository(
    name = "com_github_tidwall_buntdb",
    importpath = "github.com/tidwall/buntdb",
    sum = "h1:8KOzf5Gg97DoCMSOgcwZjnM0FfROtq0fcZkPW54oGKU=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_tidwall_gjson",
    importpath = "github.com/tidwall/gjson",
    sum = "h1:ikuZsLdhr8Ws0IdROXUS1Gi4v9Z4pGqpX/CvJkxvfpo=",
    version = "v1.12.1",
)

go_repository(
    name = "com_github_tidwall_grect",
    importpath = "github.com/tidwall/grect",
    sum = "h1:dA3oIgNgWdSspFzn1kS4S/RDpZFLrIxAZOdJKjYapOg=",
    version = "v0.1.4",
)

go_repository(
    name = "com_github_tidwall_match",
    importpath = "github.com/tidwall/match",
    sum = "h1:+Ho715JplO36QYgwN9PGYNhgZvoUSc9X2c80KVTi+GA=",
    version = "v1.1.1",
)

go_repository(
    name = "com_github_tidwall_pretty",
    importpath = "github.com/tidwall/pretty",
    sum = "h1:RWIZEg2iJ8/g6fDDYzMpobmaoGh5OLl4AXtGUGPcqCs=",
    version = "v1.2.0",
)

go_repository(
    name = "com_github_tidwall_rtred",
    importpath = "github.com/tidwall/rtred",
    sum = "h1:exmoQtOLvDoO8ud++6LwVsAMTu0KPzLTUrMln8u1yu8=",
    version = "v0.1.2",
)

go_repository(
    name = "com_github_tidwall_tinyqueue",
    importpath = "github.com/tidwall/tinyqueue",
    sum = "h1:SpNEvEggbpyN5DIReaJ2/1ndroY8iyEGxPYxoSaymYE=",
    version = "v0.1.1",
)

go_repository(
    name = "com_github_tinylib_msgp",
    importpath = "github.com/tinylib/msgp",
    sum = "h1:gWmO7n0Ys2RBEb7GPYB9Ujq8Mk5p2U08lRnmMcGy6BQ=",
    version = "v1.1.2",
)

go_repository(
    name = "com_github_tmthrgd_go_hex",
    importpath = "github.com/tmthrgd/go-hex",
    sum = "h1:9lRDQMhESg+zvGYmW5DyG0UqvY96Bu5QYsTLvCHdrgo=",
    version = "v0.0.0-20190904060850-447a3041c3bc",
)

go_repository(
    name = "com_github_tv42_httpunix",
    importpath = "github.com/tv42/httpunix",
    sum = "h1:G3dpKMzFDjgEh2q1Z7zUUtKa8ViPtH+ocF0bE0g00O8=",
    version = "v0.0.0-20150427012821-b75d8614f926",
)

go_repository(
    name = "com_github_twitchtv_twirp",
    importpath = "github.com/twitchtv/twirp",
    sum = "h1:s5WnVKMhC4Xz1jOfNAqTg85iguOWAvsrCJoPiezlLFA=",
    version = "v8.1.1+incompatible",
)

go_repository(
    name = "com_github_ugorji_go",
    importpath = "github.com/ugorji/go",
    sum = "h1:/68gy2h+1mWMrwZFeD1kQialdSzAb432dtpeJ42ovdo=",
    version = "v1.1.7",
)

go_repository(
    name = "com_github_ugorji_go_codec",
    importpath = "github.com/ugorji/go/codec",
    sum = "h1:2SvQaVZ1ouYrrKKwoSk2pzd4A9evlKJb9oTL+OaLUSs=",
    version = "v1.1.7",
)

go_repository(
    name = "com_github_urfave_negroni",
    importpath = "github.com/urfave/negroni",
    sum = "h1:kIimOitoypq34K7TG7DUaJ9kq/N4Ofuwi1sjz0KipXc=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_valyala_bytebufferpool",
    importpath = "github.com/valyala/bytebufferpool",
    sum = "h1:GqA5TC/0021Y/b9FG4Oi9Mr3q7XYx6KllzawFIhcdPw=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_valyala_fasthttp",
    importpath = "github.com/valyala/fasthttp",
    sum = "h1:keswgWzyKyNIIjz2a7JmCYHOOIkRp6HMx9oTV6QrZWY=",
    version = "v1.32.0",
)

go_repository(
    name = "com_github_valyala_fasttemplate",
    importpath = "github.com/valyala/fasttemplate",
    sum = "h1:TVEnxayobAdVkhQfrfes2IzOB6o+z4roRkPF52WA1u4=",
    version = "v1.2.1",
)

go_repository(
    name = "com_github_valyala_tcplisten",
    importpath = "github.com/valyala/tcplisten",
    sum = "h1:rBHj/Xf+E1tRGZyWIWwJDiRY0zc1Js+CV5DqwacVSA8=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_vmihailenco_bufpool",
    importpath = "github.com/vmihailenco/bufpool",
    sum = "h1:gOq2WmBrq0i2yW5QJ16ykccQ4wH9UyEsgLm6czKAd94=",
    version = "v0.1.11",
)

go_repository(
    name = "com_github_vmihailenco_msgpack_v4",
    importpath = "github.com/vmihailenco/msgpack/v4",
    sum = "h1:Q47CePddpNGNhk4GCnAx9DDtASi2rasatE0cd26cZoE=",
    version = "v4.3.11",
)

go_repository(
    name = "com_github_vmihailenco_msgpack_v5",
    importpath = "github.com/vmihailenco/msgpack/v5",
    sum = "h1:qMKAwOV+meBw2Y8k9cVwAy7qErtYCwBzZ2ellBfvnqc=",
    version = "v5.3.4",
)

go_repository(
    name = "com_github_vmihailenco_tagparser",
    importpath = "github.com/vmihailenco/tagparser",
    sum = "h1:gnjoVuB/kljJ5wICEEOpx98oXMWPLj22G67Vbd1qPqc=",
    version = "v0.1.2",
)

go_repository(
    name = "com_github_vmihailenco_tagparser_v2",
    importpath = "github.com/vmihailenco/tagparser/v2",
    sum = "h1:y09buUbR+b5aycVFQs/g70pqKVZNBmxwAhO7/IwNM9g=",
    version = "v2.0.0",
)

go_repository(
    name = "com_github_xdg_go_pbkdf2",
    importpath = "github.com/xdg-go/pbkdf2",
    sum = "h1:Su7DPu48wXMwC3bs7MCNG+z4FhcyEuz5dlvchbq0B0c=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_xdg_go_scram",
    importpath = "github.com/xdg-go/scram",
    sum = "h1:akYIkZ28e6A96dkWNJQu3nmCzH3YfwMPQExUYDaRv7w=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_xdg_go_stringprep",
    importpath = "github.com/xdg-go/stringprep",
    sum = "h1:6iq84/ryjjeRmMJwxutI51F2GIPlP5BfTvXHeYjyhBc=",
    version = "v1.0.2",
)

go_repository(
    name = "com_github_xdg_scram",
    importpath = "github.com/xdg/scram",
    sum = "h1:u40Z8hqBAAQyv+vATcGgV0YCnDjqSL7/q/JyPhhJSPk=",
    version = "v0.0.0-20180814205039-7eeb5667e42c",
)

go_repository(
    name = "com_github_xdg_stringprep",
    importpath = "github.com/xdg/stringprep",
    sum = "h1:d9X0esnoa3dFsV0FG35rAT0RIhYFlPq7MiP+DW89La0=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_youmark_pkcs8",
    importpath = "github.com/youmark/pkcs8",
    sum = "h1:splanxYIlg+5LfHAM6xpdFEAYOk8iySO56hMFq6uLyA=",
    version = "v0.0.0-20181117223130-1be2e3e5546d",
)

go_repository(
    name = "com_github_yuin_goldmark",
    importpath = "github.com/yuin/goldmark",
    sum = "h1:ruQGxdhGHe7FWOJPT0mKs5+pD2Xs1Bm/kdGlHO04FmM=",
    version = "v1.2.1",
)

go_repository(
    name = "com_github_zenazn_goji",
    importpath = "github.com/zenazn/goji",
    sum = "h1:4lbD8Mx2h7IvloP7r2C0D6ltZP6Ufip8Hn0wmSK5LR8=",
    version = "v1.0.1",
)

go_repository(
    name = "com_google_cloud_go",
    importpath = "cloud.google.com/go",
    sum = "h1:EpMNVUorLiZIELdMZbCYX/ByTFCdoYopYAGxaGVz9ms=",
    version = "v0.57.0",
)

go_repository(
    name = "com_google_cloud_go_bigquery",
    importpath = "cloud.google.com/go/bigquery",
    sum = "h1:PQcPefKFdaIzjQFbiyOgAqyx8q5djaE7x9Sqe712DPA=",
    version = "v1.8.0",
)

go_repository(
    name = "com_google_cloud_go_datastore",
    importpath = "cloud.google.com/go/datastore",
    sum = "h1:/May9ojXjRkPBNVrq+oWLqmWCkr4OU5uRY29bu0mRyQ=",
    version = "v1.1.0",
)

go_repository(
    name = "com_google_cloud_go_pubsub",
    importpath = "cloud.google.com/go/pubsub",
    sum = "h1:76oR7VBOkL7ivoIrFKyW0k7YDCRelrlxktIzQiIUGgg=",
    version = "v1.4.0",
)

go_repository(
    name = "com_google_cloud_go_storage",
    importpath = "cloud.google.com/go/storage",
    sum = "h1:86K1Gel7BQ9/WmNWn7dTKMvTLFzwtBe5FNqYbi9X35g=",
    version = "v1.8.0",
)

go_repository(
    name = "com_shuralyov_dmitri_gpu_mtl",
    importpath = "dmitri.shuralyov.com/gpu/mtl",
    sum = "h1:VpgP7xuJadIUuKccphEpTJnWhS2jkQyMt6Y7pJCD7fY=",
    version = "v0.0.0-20190408044501-666a987793e9",
)

go_repository(
    name = "im_mellium_sasl",
    importpath = "mellium.im/sasl",
    sum = "h1:nspKSRg7/SyO0cRGY71OkfHab8tf9kCts6a6oTDut0w=",
    version = "v0.2.1",
)

go_repository(
    name = "in_gopkg_check_v1",
    importpath = "gopkg.in/check.v1",
    sum = "h1:BLraFXnmrev5lT+xlilqcH8XK9/i0At2xKjWk4p6zsU=",
    version = "v1.0.0-20200227125254-8fa46927fb4f",
)

go_repository(
    name = "in_gopkg_errgo_v2",
    importpath = "gopkg.in/errgo.v2",
    sum = "h1:0vLT13EuvQ0hNvakwLuFZ/jYrLp5F3kcWHXdRggjCE8=",
    version = "v2.1.0",
)

go_repository(
    name = "in_gopkg_fsnotify_v1",
    importpath = "gopkg.in/fsnotify.v1",
    sum = "h1:xOHLXZwVvI9hhs+cLKq5+I5onOuwQLhQwiu63xxlHs4=",
    version = "v1.4.7",
)

go_repository(
    name = "in_gopkg_inconshreveable_log15_v2",
    importpath = "gopkg.in/inconshreveable/log15.v2",
    sum = "h1:RlWgLqCMMIYYEVcAR5MDsuHlVkaIPDAF+5Dehzg8L5A=",
    version = "v2.0.0-20180818164646-67afb5ed74ec",
)

go_repository(
    name = "in_gopkg_inf_v0",
    importpath = "gopkg.in/inf.v0",
    sum = "h1:73M5CoZyi3ZLMOyDlQh031Cx6N9NDJ2Vvfl76EDAgDc=",
    version = "v0.9.1",
)

go_repository(
    name = "in_gopkg_jinzhu_gorm_v1",
    importpath = "gopkg.in/jinzhu/gorm.v1",
    sum = "h1:63D1Sk0C0mhCbK930D0PkD3nKT8wLxz6lLPh5V6D2hM=",
    version = "v1.9.1",
)

go_repository(
    name = "in_gopkg_olivere_elastic_v3",
    importpath = "gopkg.in/olivere/elastic.v3",
    sum = "h1:u3B8p1VlHF3yNLVOlhIWFT3F1ICcHfM5V6FFJe6pPSo=",
    version = "v3.0.75",
)

go_repository(
    name = "in_gopkg_olivere_elastic_v5",
    importpath = "gopkg.in/olivere/elastic.v5",
    sum = "h1:acF/tRSg5geZpE3rqLglkS79CQMIMzOpWZE7hRXIkjs=",
    version = "v5.0.84",
)

go_repository(
    name = "in_gopkg_square_go_jose_v2",
    importpath = "gopkg.in/square/go-jose.v2",
    sum = "h1:7odma5RETjNHWJnR32wx8t+Io4djHE1PqxCFx3iiZ2w=",
    version = "v2.5.1",
)

go_repository(
    name = "in_gopkg_tomb_v1",
    importpath = "gopkg.in/tomb.v1",
    sum = "h1:uRGJdciOHaEIrze2W8Q3AKkepLTh2hOroT7a+7czfdQ=",
    version = "v1.0.0-20141024135613-dd632973f1e7",
)

go_repository(
    name = "in_gopkg_yaml_v2",
    importpath = "gopkg.in/yaml.v2",
    sum = "h1:D8xgwECY7CYvx+Y2n4sBz93Jn9JRvxdiyyo8CTfuKaY=",
    version = "v2.4.0",
)

go_repository(
    name = "in_gopkg_yaml_v3",
    importpath = "gopkg.in/yaml.v3",
    sum = "h1:h8qDotaEPuJATrMmW04NCwg7v22aHH28wwpauUhK9Oo=",
    version = "v3.0.0-20210107192922-496545a6307b",
)

go_repository(
    name = "io_gorm_driver_mysql",
    importpath = "gorm.io/driver/mysql",
    sum = "h1:omJoilUzyrAp0xNoio88lGJCroGdIOen9hq2A/+3ifw=",
    version = "v1.0.1",
)

go_repository(
    name = "io_gorm_driver_postgres",
    importpath = "gorm.io/driver/postgres",
    sum = "h1:Yh4jyFQ0a7F+JPU0Gtiam/eKmpT/XFc1FKxotGqc6FM=",
    version = "v1.0.0",
)

go_repository(
    name = "io_gorm_driver_sqlserver",
    importpath = "gorm.io/driver/sqlserver",
    sum = "h1:V15fszi0XAo7fbx3/cF50ngshDSN4QT0MXpWTylyPTY=",
    version = "v1.0.4",
)

go_repository(
    name = "io_gorm_gorm",
    importpath = "gorm.io/gorm",
    sum = "h1:qa7tC1WcU+DBI/ZKMxvXy1FcrlGsvxlaKufHrT2qQ08=",
    version = "v1.20.6",
)

go_repository(
    name = "io_k8s_api",
    importpath = "k8s.io/api",
    sum = "h1:H9d/lw+VkZKEVIUc8F3wgiQ+FUXTTr21M87jXLU7yqM=",
    version = "v0.17.0",
)

go_repository(
    name = "io_k8s_apimachinery",
    importpath = "k8s.io/apimachinery",
    sum = "h1:xRBnuie9rXcPxUkDizUsGvPf1cnlZCFu210op7J7LJo=",
    version = "v0.17.0",
)

go_repository(
    name = "io_k8s_client_go",
    importpath = "k8s.io/client-go",
    sum = "h1:8QOGvUGdqDMFrm9sD6IUFl256BcffynGoe80sxgTEDg=",
    version = "v0.17.0",
)

go_repository(
    name = "io_k8s_gengo",
    importpath = "k8s.io/gengo",
    sum = "h1:4s3/R4+OYYYUKptXPhZKjQ04WJ6EhQQVFdjOFvCazDk=",
    version = "v0.0.0-20190128074634-0689ccc1d7d6",
)

go_repository(
    name = "io_k8s_klog",
    importpath = "k8s.io/klog",
    sum = "h1:Pt+yjF5aB1xDSVbau4VsWe+dQNzA0qv1LlXdC2dF6Q8=",
    version = "v1.0.0",
)

go_repository(
    name = "io_k8s_kube_openapi",
    importpath = "k8s.io/kube-openapi",
    sum = "h1:UcxjrRMyNx/i/y8G7kPvLyy7rfbeuf1PYyBf973pgyU=",
    version = "v0.0.0-20191107075043-30be4d16710a",
)

go_repository(
    name = "io_k8s_sigs_structured_merge_diff",
    importpath = "sigs.k8s.io/structured-merge-diff",
    sum = "h1:4Z09Hglb792X0kfOBBJUPFEyvVfQWrYT/l8h5EKA6JQ=",
    version = "v0.0.0-20190525122527-15d366b2352e",
)

go_repository(
    name = "io_k8s_sigs_yaml",
    importpath = "sigs.k8s.io/yaml",
    sum = "h1:4A07+ZFc2wgJwo8YNlQpr1rVlgUDlxXHhPJciaPY5gs=",
    version = "v1.1.0",
)

go_repository(
    name = "io_k8s_utils",
    importpath = "k8s.io/utils",
    sum = "h1:GiPwtSzdP43eI1hpPCbROQCCIgCuiMMNF8YUVLF3vJo=",
    version = "v0.0.0-20191114184206-e782cd3c129f",
)

go_repository(
    name = "io_opencensus_go",
    importpath = "go.opencensus.io",
    sum = "h1:LYy1Hy3MJdrCdMwwzxA/dRok4ejH+RwNGbuoD9fCjto=",
    version = "v0.22.4",
)

go_repository(
    name = "io_opentelemetry_go_otel",
    importpath = "go.opentelemetry.io/otel",
    sum = "h1:IN2tzQa9Gc4ZVKnTaMbPVcHjvzOdg5n9QfnmlqiET7E=",
    version = "v0.11.0",
)

go_repository(
    name = "io_rsc_binaryregexp",
    importpath = "rsc.io/binaryregexp",
    sum = "h1:HfqmD5MEmC0zvwBuF187nq9mdnXjXsSivRiXN7SmRkE=",
    version = "v0.2.0",
)

go_repository(
    name = "io_rsc_quote_v3",
    importpath = "rsc.io/quote/v3",
    sum = "h1:9JKUTTIUgS6kzR9mK1YuGKv6Nl+DijDNIc0ghT58FaY=",
    version = "v3.1.0",
)

go_repository(
    name = "io_rsc_sampler",
    importpath = "rsc.io/sampler",
    sum = "h1:7uVkIFmeBqHfdjD+gZwtXXI+RODJ2Wc4O7MPEh/QiW4=",
    version = "v1.3.0",
)

go_repository(
    name = "org_golang_google_api",
    importpath = "google.golang.org/api",
    sum = "h1:BaiDisFir8O4IJxvAabCGGkQ6yCJegNQqSVoYUNAnbk=",
    version = "v0.29.0",
)

go_repository(
    name = "org_golang_google_appengine",
    importpath = "google.golang.org/appengine",
    sum = "h1:lMO5rYAqUxkmaj76jAkRUvt5JZgFymx/+Q5Mzfivuhc=",
    version = "v1.6.6",
)

go_repository(
    name = "org_golang_google_genproto",
    importpath = "google.golang.org/genproto",
    sum = "h1:HJaAqDnKreMkv+AQyf1Mcw0jEmL9kKBNL07RDJu1N/k=",
    version = "v0.0.0-20200726014623-da3ae01ef02d",
)

go_repository(
    name = "org_golang_google_grpc",
    importpath = "google.golang.org/grpc",
    sum = "h1:zWTV+LMdc3kaiJMSTOFz2UgSBgx8RNQoTGiZu3fR9S0=",
    version = "v1.32.0",
)

go_repository(
    name = "org_golang_google_protobuf",
    importpath = "google.golang.org/protobuf",
    sum = "h1:SnqbnDw1V7RiZcXPx5MEeqPv2s79L9i7BJUlG/+RurQ=",
    version = "v1.27.1",
)

go_repository(
    name = "org_golang_x_crypto",
    importpath = "golang.org/x/crypto",
    sum = "h1:7I4JAnoQBe7ZtJcBaYHi5UtiO8tQHbUSXxL+pnGRANg=",
    version = "v0.0.0-20210921155107-089bfa567519",
)

go_repository(
    name = "org_golang_x_exp",
    importpath = "golang.org/x/exp",
    sum = "h1:5XVKs2rlCg8EFyRcvO8/XFwYxh1oKJO1Q3X5vttIf9c=",
    version = "v0.0.0-20200908183739-ae8ad444f925",
)

go_repository(
    name = "org_golang_x_image",
    importpath = "golang.org/x/image",
    sum = "h1:+qEpEAPhDZ1o0x3tHzZTQDArnOixOzGD9HUJfcg0mb4=",
    version = "v0.0.0-20190802002840-cff245a6509b",
)

go_repository(
    name = "org_golang_x_lint",
    importpath = "golang.org/x/lint",
    sum = "h1:Wh+f8QHJXR411sJR8/vRBTZ7YapZaRvUcLFFJhusH0k=",
    version = "v0.0.0-20200302205851-738671d3881b",
)

go_repository(
    name = "org_golang_x_mobile",
    importpath = "golang.org/x/mobile",
    sum = "h1:4+4C/Iv2U4fMZBiMCc98MG1In4gJY5YRhtpDNeDeHWs=",
    version = "v0.0.0-20190719004257-d2bd2a29d028",
)

go_repository(
    name = "org_golang_x_mod",
    importpath = "golang.org/x/mod",
    sum = "h1:xUIPaMhvROX9dhPvRCenIJtU78+lbEenGbgqB5hfHCQ=",
    version = "v0.3.1-0.20200828183125-ce943fd02449",
)

go_repository(
    name = "org_golang_x_net",
    importpath = "golang.org/x/net",
    sum = "h1:A0lJIi+hcTR6aajJH4YqKWwohY4aW9RO7oRMcdv+HKI=",
    version = "v0.0.0-20211020060615-d418f374d309",
)

go_repository(
    name = "org_golang_x_oauth2",
    importpath = "golang.org/x/oauth2",
    sum = "h1:TzXSXBo42m9gQenoE3b9BGiEpg5IG2JkU5FkPIawgtw=",
    version = "v0.0.0-20200107190931-bf48bf16ab8d",
)

go_repository(
    name = "org_golang_x_sync",
    importpath = "golang.org/x/sync",
    sum = "h1:SQFwaSi55rU7vdNs9Yr0Z324VNlrF+0wMqRXT4St8ck=",
    version = "v0.0.0-20201020160332-67f06af15bc9",
)

go_repository(
    name = "org_golang_x_sys",
    importpath = "golang.org/x/sys",
    sum = "h1:nhht2DYV/Sn3qOayu8lM+cU1ii9sTLUeBQwQQfUHtrs=",
    version = "v0.0.0-20220227234510-4e6760a101f9",
)

go_repository(
    name = "org_golang_x_term",
    importpath = "golang.org/x/term",
    sum = "h1:JGgROgKl9N8DuW20oFS5gxc+lE67/N3FcwmBPMe7ArY=",
    version = "v0.0.0-20210927222741-03fcf44c2211",
)

go_repository(
    name = "org_golang_x_text",
    importpath = "golang.org/x/text",
    sum = "h1:olpwvP2KacW1ZWvsR7uQhoyTYvKAupfQrRGBFM352Gk=",
    version = "v0.3.7",
)

go_repository(
    name = "org_golang_x_time",
    importpath = "golang.org/x/time",
    sum = "h1:GZokNIeuVkl3aZHJchRrr13WCsols02MLUcz1U9is6M=",
    version = "v0.0.0-20211116232009-f0f3c7e86c11",
)

go_repository(
    name = "org_golang_x_tools",
    importpath = "golang.org/x/tools",
    sum = "h1:4nW4NLDYnU28ojHaHO8OVxFHk/aQ33U01a9cjED+pzE=",
    version = "v0.0.0-20201224043029-2b0845dc783e",
)

go_repository(
    name = "org_golang_x_xerrors",
    importpath = "golang.org/x/xerrors",
    sum = "h1:go1bK/D/BFZV2I8cIQd1NKEZ+0owSTG1fDTci4IqFcE=",
    version = "v0.0.0-20200804184101-5ec99f83aff1",
)

go_repository(
    name = "org_mongodb_go_mongo_driver",
    importpath = "go.mongodb.org/mongo-driver",
    sum = "h1:9nOVLGDfOaZ9R0tBumx/BcuqkbFpyTCU2r/Po7A2azI=",
    version = "v1.5.1",
)

go_repository(
    name = "org_uber_go_atomic",
    importpath = "go.uber.org/atomic",
    sum = "h1:Ezj3JGmsOnG1MoRWQkPBsKLe9DwWD9QeXzTRzzldNVk=",
    version = "v1.6.0",
)

go_repository(
    name = "org_uber_go_multierr",
    importpath = "go.uber.org/multierr",
    sum = "h1:KCa4XfM8CWFCpxXRGok+Q0SS/0XBhMDbHHGABQLvD2A=",
    version = "v1.5.0",
)

go_repository(
    name = "org_uber_go_tools",
    importpath = "go.uber.org/tools",
    sum = "h1:0mgffUl7nfd+FpvXMVz4IDEaUSmT1ysygQC7qYo7sG4=",
    version = "v0.0.0-20190618225709-2cfd321de3ee",
)

go_repository(
    name = "org_uber_go_zap",
    importpath = "go.uber.org/zap",
    sum = "h1:nR6NoDBgAf67s68NhaXbsojM+2gxp3S1hWkHDl27pVU=",
    version = "v1.13.0",
)

go_rules_dependencies()

go_register_toolchains(version = "1.18")

gazelle_dependencies()
