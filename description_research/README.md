# Configuration Descriptions

The goal of this effort is to provide **high-quality, comparable descriptions** for as many tracer configuration keys as we can.

To do that we want to leverage LLMs and run them in a **multi-step pipeline** with **reproducible output** (same inputs ⇒ same JSON structure and stable ordering).

## Goal & Results

The end result we want is a set of JSON outputs (one per step) containing **all configuration keys** plus **candidate descriptions** coming from different sources.
This makes it possible to compare descriptions across sources (and potentially across languages) and choose the best final phrasing.

## Process

We do this in multiple steps (as if it were an automated pipeline).
Each step:

- **takes an input file** (the previous step output, or the initial key list)
- **produces an output file** used by the next step or by a developer for review

### What the step scripts must output

Each step is implemented as a script. The script’s primary output is a **single JSON file** matching the schema below:

- Steps 1–3 are **extraction steps**:
  - Do **not** invent or paraphrase descriptions; copy the best available text from the source (minor whitespace/format cleanup is OK).
  - Any script logs should go to stderr; the JSON file must contain only JSON.
- Keep outputs **stable**:
  - `documentedConfigurations` sorted by `key`, then `implementation`
  - `missingConfigurations` sorted by `key`, then `implementation`
  - `results` ordered by preference (registry → same language docs → other language docs → llm_generated)
- Counters must match arrays:
  - `documentedCount == len(documentedConfigurations)`
  - `missingCount == len(missingConfigurations)`

### Output format

Each step output should have a name fitting this pattern: `configurations_descriptions_step_{{step_id}}.json`

The content of the file itself should be:

```json
{
  "lang": "golang",
  "missingCount": 2,
  "documentedCount": 1,
  "documentedConfigurations": [
    {
      "key": "DD_ACTION_EXECUTION_ID",
      "implementation": "A",
      "results": [
        {
          "description": "This is the description found by the step and it gives context on how to use the key.",
          "shortDescription": "",
          "source": "documentation_same_language"
        },
        {
          "description": "This is the description found by the step and it gives context on how to use the key.",
          "shortDescription": "",
          "source": "documentation_other_language"
        }
      ],
      "missingSources": [
        {
          "source": "registry_doc",
          "reason": "quality"
        }
      ]
    }
  ],
  "missingConfigurations": [
    {
      "key": "DD_MY_KEY_WITH_NO_DESCRIPTION",
      "implementation": "A",
      "missingReasons": [
        {
          "source": "registry_doc",
          "reason": "quality"
        },
        {
          "source": "documentation_same_language",
          "reason": "not_found"
        }
      ]
    }
  ]
}
```

- `lang`: The language for which the pipeline ran (e.g. `golang`).
- `missingCount` / `documentedCount`: Counts of missing vs documented items. Must match array lengths.
- `documentedConfigurations`: Documented key+implementation entries.
  - Each entry represents a **key+implementation** pair from the tracer's `supported_configurations.json` (in this repo: `internal/env/supported_configurations.json`).
  - `implementation` must be copied as-is from the supported configurations data (e.g. `"A"`, `"B"`, ...).
  - `results`: A list of candidate descriptions found in different sources.
    - `description`: The extracted text (steps 1–3) or generated text (step 4).
    - `shortDescription`: Always present as a string. For steps 1–3 it should be `""`. Step 4 may fill it for `llm_generated` results.
    - `source`: Where the description came from.
- `missingConfigurations`: Undocumented key+implementation entries, with explanations of why this step did not produce a usable description.
  - `missingReasons`: An array of source+reason pairs for this key.
  - `missingSources` (on documented entries): Optional bookkeeping for sources that were attempted but rejected.

The sources we want to use for now are:

- `registry_doc` when extracted from the registry data
- `documentation_same_language` when extracted from the documentation, reading the correct language existing documentation
- `documentation_other_language`  when extracted from the documentation reading other languages existing documentation
- `llm_generated` when generated using an LLM by understanding how the configuration key is used

`missingReasons` `reason` attribute can have the following values:

- `not_found` when nothing is found
- `quality` when the quality of the data is not good enough (too short, not specific, or not a real description)

### Quality bar (steps 1–3)

Steps 1–3 are *extraction steps*; they should reject low-quality text. A description is considered usable if:

- It is **specific**: says what the configuration controls (not just “enables feature X” without context).
- It is **self-contained**: makes sense without requiring readers to “see docs” or click elsewhere.
- It is **not trivially short** (default heuristic: at least 20 characters, but use judgment; many keys need more).

## Steps

### 1 - Registry documentation

Label: `registry_doc`

Registry current data is available here: https://dd-feature-parity.azurewebsites.net/configurations/

The very first step of the pipeline should retrieve the data available there and use it to extract descriptions when possible.

If no documentation is found, or the documentation is lacking quality (e.g. less than 20 characters or obviously incomplete), it should be marked as such with `missingReasons` / `missingSources` using:

- `reason: "not_found"` when the registry has no description
- `reason: "quality"` when a description exists but is not usable

#### What the AI should do

Generate a **step 1 script** which produces `configurations_descriptions_step_1.json` by joining:

- the tracer key list from `internal/env/supported_configurations.json` (keys + `implementation` letters)
- the registry JSON from `https://dd-feature-parity.azurewebsites.net/configurations/`

The script must be deterministic and safe (read-only inputs, write-only output).

**Script contract (expected by the pipeline):**

- Inputs (CLI args or constants):
  - `--lang` (example: `golang`)
  - `--supported-configurations` (default: `internal/env/supported_configurations.json`)
  - `--output` (directory where the output `configurations_descriptions_step_1.json` will be produced. Default: ./result)
- Output:
  - JSON file matching the schema defined above.

**Registry parsing requirements:**

- The registry endpoint returns a JSON array. Each element has:
  - `name` (configuration key, e.g. `DD_AGENT_HOST`)
  - `configurations[]`, where each entry can include:
    - `version` (e.g. `"A"`, `"B"`, `"C"`) which maps to our `implementation`
    - `description` (may be `null`, `"null"`, empty string, or real text)
    - `implementations[]` with `language` (e.g. `"golang"`, `"java"`, ...)
- Build an index `registryByKey[name]`.

**Per key+implementation behavior:**

For every key+implementation from `supported_configurations.json`:

- Locate the registry entry:
  - If not present: mark missing with `{ "source": "registry_doc", "reason": "not_found" }`.
- Choose a registry configuration record deterministically:
  - Prefer a record where `version == implementation`.
    - If multiple records match, prefer one whose `implementations[]` includes `language == lang`.
    - If still tied, pick the first one in the registry payload (stable input ⇒ stable output).
  - If no record matches `version == implementation`, fall back to:
    - a record whose `implementations[]` includes `language == lang`, else
    - the first record with a non-empty `description`, else
    - mark missing with `reason: "not_found"`.
- Extract `description`:
  - Treat `null`, `"null"`, empty/whitespace, or anything that fails the quality bar as `reason: "quality"`.
  - Otherwise, produce a `results` entry:
    - `source: "registry_doc"`
    - `description`: exact extracted text (trim whitespace)
    - `shortDescription: ""`

**Output assembly requirements:**

- Start from the full set of key+implementation pairs (so every supported key appears exactly once across `documentedConfigurations` or `missingConfigurations`).
- Ensure stable ordering and correct counts as described in “What the step scripts must output”.

### 2 - Documentation - same language

Label: `documentation_same_language`

This step attempts to find descriptions in **existing tracer documentation for the same language as `lang`**.
Note: `lang` here refers to the tracer language (e.g. Go/Java/Ruby), not to documentation translation languages.

#### Inputs

- The previous step output `configurations_descriptions_step_1.json`
- A local checkout of the Datadog documentation repository (or the ability to clone it): `https://github.com/DataDog/documentation`.

#### What the AI should do

Generate a **step 2 script** which reads step 1 output and produces `configurations_descriptions_step_2.json` by extracting descriptions from the Datadog documentation repo.

**Script contract (expected by the pipeline):**

- Inputs (CLI args or constants):
  - `--lang` (example: `golang`)
  - `--input` (path to `configurations_descriptions_step_1.json`)
  - `--docs-repo` (path to local `DataDog/documentation` checkout)
  - `--output` (directory where the output `configurations_descriptions_step_2.json` will be produced. Default: ./result)
- Output:
  - JSON file matching the schema defined above.

**Documentation corpus selection (deterministic):**

- Scan documentation source files (e.g. `**/*.md`, `**/*.mdx`, `**/*.yaml`, `**/*.yml`) under the docs repo.
- Prefer scanning files that are likely to describe tracer configuration:
  - paths containing `tracing`, `apm`, `agent`, `serverless`, or `profiling`
  - and paths that mention the tracer language (e.g. `go`, `golang`, `java`, `ruby`, ...)
  - but still allow fallback to “scan everything” if nothing matches.

**Per key extraction behavior:**

For every entry in `missingConfigurations` from step 1:

- Search the documentation corpus for the exact configuration `key` (case-sensitive).
- If multiple matches exist, select the best match using a deterministic scoring rule:
  - Prefer file paths containing tracer docs keywords: `tracing`, `apm`, `agent`, `serverless`, `profiling`.
  - Prefer file paths containing the tracer language hint for `lang` (e.g. `go`, `golang` for `golang`).
  - De-prioritize file paths that look like changelogs/release notes (e.g. `release`, `changelog`).
  - Tie-break by lexicographic file path, then by earliest match position within the file.
- Extract the smallest useful, self-contained description deterministically:
  - Take the paragraph surrounding the chosen match (bounded by blank lines), then trim.
  - If the “paragraph” is actually a code block (starts with ``` or is indented code), move to the nearest adjacent non-code paragraph and use that instead.
  - Prefer the sentence/paragraph that explains **what the key controls** and (when present) **how to format its value**.
  - Do **not** paraphrase in this step; keep the text close to the documentation wording, but you may remove formatting artifacts (e.g. bullet markers, surrounding quotes) as long as meaning is preserved.
  - Do not include large tables or unrelated sections; keep it concise.
  - Reject extracted text that fails the quality bar.

**Promotion bookkeeping:**

- When a key moves from `missingConfigurations` to `documentedConfigurations`, the script should preserve previous missing info:
  - Convert any prior `missingReasons` from step 1 into `missingSources` on the documented entry.

#### Output rules

- Create `configurations_descriptions_step_2.json` using the same schema.
- For keys documented in this step:
  - Move them from `missingConfigurations` to `documentedConfigurations`.
  - Add a `results` entry with `source: "documentation_same_language"`.
  - Ensure `shortDescription: ""` in this step.
- For keys still missing:
  - Keep them in `missingConfigurations`.
  - Add a `missingReasons` entry for `source: "documentation_same_language"` with:
    - `reason: "not_found"` if the key isn't mentioned in the docs corpus
    - `reason: "quality"` if the key is mentioned but the surrounding text is not a usable description (fails the quality bar)

#### Notes / edge cases

- If the docs describe an **alias** (e.g. `DD-API-KEY`) instead of the canonical key, treat it as a match for the canonical key and document the canonical key.
- If the docs cover multiple keys in one paragraph, it is acceptable to reuse the same paragraph for each relevant key as long as it remains accurate and self-contained.
- If a key has multiple `implementation` entries, document each key+implementation pair identically unless the docs explicitly distinguish between them.

### 3 - Documentation - other language

Label: `documentation_other_language`

This step attempts to find descriptions in documentation for **other languages** (but for the same Datadog product/feature).
It is useful when a configuration key is global/cross-language but the same-language docs are missing or incomplete.

#### Inputs

- The previous step output `configurations_descriptions_step_2.json`
- A local checkout of the Datadog documentation repository (or the ability to clone it): `https://github.com/DataDog/documentation`

#### What the AI should do

Generate a **step 3 script** which reads step 2 output and produces `configurations_descriptions_step_3.json` by extracting descriptions from *other tracer language* documentation.

Note: “other language” here means **other tracer languages** (Go vs Java vs Ruby vs …), not documentation translation languages.

**Script contract (expected by the pipeline):**

- Inputs (CLI args or constants):
  - `--lang` (the current tracer language, example: `golang`)
  - `--other-langs` (comma-separated tracer languages to search, e.g. `java,ruby,python,nodejs,dotnet,php`; must exclude `--lang`)
  - `--input` (path to `configurations_descriptions_step_2.json`)
  - `--docs-repo` (path to local `DataDog/documentation` checkout)
  - `--output` (directory where the output `configurations_descriptions_step_3.json` will be produced. Default: ./result)
- Output:
  - JSON file matching the schema defined above.

**Documentation corpus selection (deterministic):**

- Scan documentation source files (e.g. `**/*.md`, `**/*.mdx`, `**/*.yaml`, `**/*.yml`) under the docs repo.
- Prefer scanning files likely to describe tracer configuration, as in step 2, but targeting `--other-langs`:
  - paths containing tracer docs keywords: `tracing`, `apm`, `agent`, `serverless`, `profiling`
  - paths containing any of the tracer language hints for `--other-langs`
  - de-prioritize file paths that look like changelogs/release notes (e.g. `release`, `changelog`)
  - allow fallback to “scan everything” if nothing matches

**Per key extraction behavior:**

For every entry still present in `missingConfigurations`:

- Search the documentation corpus for the exact configuration `key` (case-sensitive). If not found, optionally retry with known aliases (from step 1 registry payload if available, or tracer aliases if the pipeline provides them).
- If multiple matches exist, select the best match using a deterministic scoring rule:
  - Prefer file paths containing tracer docs keywords: `tracing`, `apm`, `agent`, `serverless`, `profiling`.
  - Prefer file paths containing any language hint for `--other-langs`.
  - De-prioritize file paths that contain the `--lang` hint (to avoid accidentally re-selecting same-language docs).
  - De-prioritize file paths that look like changelogs/release notes (e.g. `release`, `changelog`).
  - Tie-break by lexicographic file path, then by earliest match position within the file.
- Extract the smallest useful, self-contained description deterministically (same rules as step 2):
  - Take the paragraph surrounding the chosen match (bounded by blank lines), then trim.
  - If the “paragraph” is actually a code block (starts with ``` or is indented code), move to the nearest adjacent non-code paragraph and use that instead.
  - Reject extracted text that fails the quality bar.
- Apply an additional “other-language” quality check:
  - If the extracted text is clearly language-specific and does not describe the configuration in a generally applicable way, treat it as `reason: "quality"` for this step.

**Promotion bookkeeping:**

- When a key moves from `missingConfigurations` to `documentedConfigurations`, preserve previous missing info:
  - Convert any prior `missingReasons` (from steps 1 and/or 2) into `missingSources` on the documented entry.

#### Output rules

- Create `configurations_descriptions_step_3.json` using the same schema.
- For keys documented in this step:
  - Move them from `missingConfigurations` to `documentedConfigurations`.
  - Add a `results` entry with `source: "documentation_other_language"`.
  - Ensure `shortDescription: ""` in this step.
- For keys still missing:
  - Keep them in `missingConfigurations`.
  - Add a `missingReasons` entry for `source: "documentation_other_language"` with:
    - `reason: "not_found"` if the key (or aliases) aren't mentioned
    - `reason: "quality"` if mentioned but the extracted text is not a usable description

### 4 - LLM generated

Label: `llm_generated`

This is the fallback step for configurations that still have no usable extracted description.
The LLM generates text by understanding **how the configuration is used**.

#### Inputs

- The previous step output `configurations_descriptions_step_3.json`
- For each remaining `key`, enough technical context to explain it accurately (the pipeline should provide this to the AI), for example:
  - where the key is read
  - how it is parsed (type, allowed values, default behavior)
  - what behavior it controls
  - any constraints, deprecations, or compatibility notes

#### What the AI should do

For every entry still present in `missingConfigurations`:

- Generate a **long description** (`description`) that is:
  - accurate given the provided context
  - user-facing (explains effect and typical usage)
  - concise (prefer 1–3 sentences unless the setting is complex)
- Generate a **short description** (`shortDescription`) as a one-liner summary (roughly 6–14 words).

#### Output rules

- Create `configurations_descriptions_step_4.json` using the same schema.
- For keys documented in this step:
  - Move them from `missingConfigurations` to `documentedConfigurations`.
  - Add a `results` entry with `source: "llm_generated"` and both `description` and `shortDescription` filled.
- `shortDescription` may remain `""` for extracted results from earlier steps (unless the pipeline decides to run a dedicated summarization step later). For `llm_generated` results, it must be non-empty.

