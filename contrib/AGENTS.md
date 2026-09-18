**BEFORE writing or editing ANY contrib code**, read the `apm-integrations` skill
([.agents/skills/apm-integrations/SKILL.md](../.agents/skills/apm-integrations/SKILL.md)). It holds
the authoring workflow, the rules that are easiest to get wrong, and links into the two guides:

* [INTEGRATIONS.md](./INTEGRATIONS.md) -- authoring an integration.
* [ORCHESTRION.md](./ORCHESTRION.md) -- auto-instrumentation with Orchestrion.
* [README.md](./README.md) -- short reference: what integrations are and how to use one.

New integrations MUST support Orchestrion auto-instrumentation and MUST include
`internal/orchestrion/_integration` tests.

## Updating Documentation

Keep this file short. Authoring rules go in [INTEGRATIONS.md](./INTEGRATIONS.md), or
[ORCHESTRION.md](./ORCHESTRION.md) for auto-instrumentation. Update the skill when the workflow or
one of the rules it states changes, and update the guides when a convention changes.
[README.md](./README.md) stays the short user-facing reference.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.
