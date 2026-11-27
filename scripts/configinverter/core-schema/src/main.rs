// Copyright 2026-Present Datadog, Inc. https://www.datadoghq.com/
// SPDX-License-Identifier: Apache-2.0

use std::{env, error::Error, io};

use libdd_config_core::{
    BooleanSyntax, ConfigSchema, ParsePolicy, Resolution, ResolveInputs, TransformRegistry,
};

fn main() -> Result<(), Box<dyn Error>> {
    let path = env::args_os()
        .nth(1)
        .ok_or("expected a configuration manifest path")?;
    let schema = ConfigSchema::from_path(
        path,
        ParsePolicy {
            booleans: BooleanSyntax::Go,
            ..ParsePolicy::default()
        },
        &TransformRegistry::with_builtins(),
    )?;
    let Resolution::Complete(snapshot) = schema.resolve(ResolveInputs::default())? else {
        return Err("Go schema requires host transforms during generation".into());
    };
    if !snapshot.diagnostics().is_empty() {
        return Err(format!(
            "invalid configuration defaults: {:?}",
            snapshot.diagnostics()
        )
        .into());
    }
    let keys: Vec<_> = snapshot.settings().map(|setting| setting.name).collect();
    serde_json::to_writer(io::stdout().lock(), &keys)?;
    Ok(())
}
