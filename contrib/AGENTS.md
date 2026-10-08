**BEFORE writing or editing ANY contrib code**, read the `apm-integrations` skill
([.agents/skills/apm-integrations/SKILL.md](../.agents/skills/apm-integrations/SKILL.md)). It holds
the authoring workflow and links into the two guides, which hold the rules:

* [INTEGRATIONS.md](./INTEGRATIONS.md) -- authoring an integration.
* [ORCHESTRION.md](./ORCHESTRION.md) -- auto-instrumentation with Orchestrion.
* [README.md](./README.md) -- short reference: what integrations are and how to use one.

New integrations MUST support Orchestrion auto-instrumentation and MUST include
`internal/orchestrion/_integration` tests.

## Updating Documentation

Keep this file short. Authoring rules go in [INTEGRATIONS.md](./INTEGRATIONS.md), or
[ORCHESTRION.md](./ORCHESTRION.md) for auto-instrumentation. Update the skill when its workflow
steps change, and update the guides when a convention changes. When an `orchestrion.yml` uses a
template variable or join point that [ORCHESTRION.md](./ORCHESTRION.md) does not list, add it there.
[README.md](./README.md) stays the short user-facing reference.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.
