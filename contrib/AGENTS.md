**BEFORE writing or editing ANY contrib code**, you MUST read [INTEGRATIONS.md](./INTEGRATIONS.md) in
full, the guide to authoring an integration. For auto-instrumentation, you MUST also read
[ORCHESTRION.md](./ORCHESTRION.md). [README.md](./README.md) is the short reference for naming and the
list of existing integrations.

New integrations MUST support Orchestrion auto-instrumentation and MUST include
`internal/orchestrion/_integration` tests. See [ORCHESTRION.md](./ORCHESTRION.md).

## Updating Documentation

Keep this file short. Put authoring rules in [INTEGRATIONS.md](./INTEGRATIONS.md), or
[ORCHESTRION.md](./ORCHESTRION.md) for auto-instrumentation, and update those when a convention
changes. [README.md](./README.md) stays the short reference for naming and existing integrations.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.
