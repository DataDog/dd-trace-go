// Copyright 2026-Present Datadog, Inc. https://www.datadoghq.com/
// SPDX-License-Identifier: Apache-2.0

use std::process::Command;

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
