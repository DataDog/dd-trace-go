// Copyright 2026-Present Datadog, Inc. https://www.datadoghq.com/
// SPDX-License-Identifier: Apache-2.0

use std::process::Command;

use libdd_config_core::{
    ConfigSchema, Origin, ParsePolicy, Resolution, ResolveInputs, TransformRegistry, Value,
};

const TRACER_MANIFEST: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../../internal/env/supported_configurations.json"
);

#[test]
fn refuses_to_generate_keys_when_a_default_is_disallowed() {
    let output = Command::new(env!("CARGO_BIN_EXE_ddtrace-go-core-schema"))
        .arg(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/disallowed-default.json"
        ))
        .output()
        .unwrap();

    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    let stderr = String::from_utf8(output.stderr).unwrap();
    assert!(stderr.contains("DD_POSITIVE_INTEGER"), "{stderr}");
    assert!(stderr.contains("DisallowedValue"), "{stderr}");
}

#[test]
fn emits_typed_startup_settings_from_the_tracer_manifest() {
    let output = Command::new(env!("CARGO_BIN_EXE_ddtrace-go-core-schema"))
        .arg(TRACER_MANIFEST)
        .output()
        .unwrap();

    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let settings: Vec<serde_json::Value> = serde_json::from_slice(&output.stdout).unwrap();
    let setting = |name: &str| {
        settings
            .iter()
            .find(|setting| setting["name"] == name)
            .unwrap()
    };
    assert_eq!(setting("DD_TRACE_STARTUP_LOGS")["property"], "logStartup");
    assert_eq!(
        setting("DD_TRACE_STARTUP_LOGS")["resolved"]["value"]["value"],
        true
    );
    assert_eq!(
        setting("OTEL_BLRP_MAX_QUEUE_SIZE")["property"],
        "blrpMaxQueueSize"
    );
    assert_eq!(
        setting("OTEL_BLRP_MAX_QUEUE_SIZE")["resolved"]["value"]["value"],
        2048
    );
    assert_eq!(setting("DD_SITE")["property"], "site");
    assert_eq!(
        setting("DD_SITE")["resolved"]["value"]["value"],
        "datadoghq.com"
    );
    assert_eq!(setting("DD_SERVICE")["property"], "serviceName");
    assert!(setting("DD_SERVICE")["resolved"].is_null());
}

#[test]
fn resolves_tracer_programmatic_names_to_the_same_settings_as_environment() {
    let schema = ConfigSchema::from_path(
        TRACER_MANIFEST,
        ParsePolicy::default(),
        &TransformRegistry::with_builtins(),
    )
    .unwrap();
    let inputs: ResolveInputs = serde_json::from_value(serde_json::json!({
        "layers": [
            {
                "origin": "env_var",
                "entries": [{"name": "DD_SERVICE", "value": "environment"}]
            },
            {
                "origin": "code",
                "entries": [
                    {"name": "service", "value": {"kind": "string", "value": "programmatic"}},
                    {"name": "env", "value": {"kind": "string", "value": "prod"}},
                    {"name": "version", "value": {"kind": "string", "value": "1.2.3"}},
                    {"name": "startupLogs", "value": {"kind": "boolean", "value": false}},
                    {"name": "runtimeMetrics.enabled", "value": {"kind": "boolean", "value": true}}
                ]
            }
        ]
    }))
    .unwrap();
    let Resolution::Complete(snapshot) = schema.resolve(inputs).unwrap() else {
        panic!("configuration requires a host transform");
    };
    assert!(
        snapshot.diagnostics().is_empty(),
        "{:?}",
        snapshot.diagnostics()
    );
    for (key, value) in [
        ("DD_SERVICE", Value::String("programmatic".into())),
        ("DD_ENV", Value::String("prod".into())),
        ("DD_VERSION", Value::String("1.2.3".into())),
        ("DD_TRACE_STARTUP_LOGS", Value::Boolean(false)),
        ("DD_RUNTIME_METRICS_ENABLED", Value::Boolean(true)),
    ] {
        let resolved = snapshot.get(key).unwrap();
        assert_eq!(resolved.origin, Origin::Code, "{key}");
        assert_eq!(resolved.value, value, "{key}");
    }
}
