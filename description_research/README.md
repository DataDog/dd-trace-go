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

#### Implementation note (current)

The Datadog documentation repo is not reliably parseable with deterministic scripts (shortcodes, partials, mixed formats, tables, etc.).
So step 2 is implemented as an **LLM-assisted extraction step**:

- a deterministic script produces an LLM-fillable overrides JSON listing the missing key+implementation pairs
- an LLM fills that file by searching the docs repo and copying the best available description text (no invention)
- a deterministic materializer merges those overrides into step 1 output to produce `configurations_descriptions_step_2.json`

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

At the moment, step 4 is implemented as:

- a deterministic **key extraction** script producing an LLM-fillable JSON file
- an LLM editing that JSON file in-place (reviewable data)
- a deterministic **merger** script that applies those LLM-generated descriptions back into a step output JSON

#### Inputs

- A step output JSON to merge into (typically `configurations_descriptions_step_2.json` today; step 3 is not implemented yet)
- A checkout of this repository (so the LLM can read code to stay accurate)

#### What to do

##### 4a — Extract LLM-needed keys (deterministic)

Before running the LLM, we must identify which key+implementation pairs still need **LLM-generated** descriptions.

LLM needing keys are:

- `documentedConfigurations` keys with **no** `results` entry (missing or empty array)
- `documentedConfigurations` keys with **no** result whose `source` is `"registry_doc"`
- `missingConfigurations` keys

To extract those keys deterministically, use:

```shell
cd description_research
python3 step_4a_extract_llm_needed_keys.py \
  --input ./result/configurations_descriptions_step_2.json \
  --output ./result/configurations_llm_needed_keys.json
```

The output file groups the pairs into the three buckets above under `llmNeeded.*`.
Each pair object contains a `"description": ""` field intended to be filled by an LLM.

##### 4b — Fill `configurations_llm_needed_keys.json` with an LLM (reviewable data)

Run the LLM **in Cursor** with access to this repository. Edit in place:

- Input/output file: `@description_research/result/configurations_llm_needed_keys.json`

Rules:

- Use this repo’s code as the source of truth (do not guess).
- If you can’t determine behavior confidently, leave `"description": ""`.
- Keep descriptions user-facing: **1–3 sentences**, self-contained, specific, not trivially short.
- Do not change `key` / `implementation` values.

- where the key is read (package/file/function if available)
- how it is parsed (type, allowed values, default behavior)
- what behavior it controls
- any constraints, deprecations, or compatibility notes


##### 4b - Produce overrides with an LLM (reviewable data)

Run the LLM **in Cursor** (model: `gpt-5.2-high`) to produce a single overrides file:
`description_research/configurations_descriptions_step_4_overrides.json`.

This overrides file is the only non-deterministic artifact. It should be reviewed like any other change.

**How to run the LLM in Cursor (directive procedure):**

- Ensure `description_research/configurations_descriptions_step_4_context.json` exists (produced by step 4a).
- Decide a deterministic batching strategy (required because the context packet can be large):
  - Sort entries by `key`, then `implementation`.
  - Process in fixed-size batches (e.g. 50 entries per batch).
  - Keep a record of the batch boundaries (e.g. first/last key) in your PR/notes.
- For each batch, start a Cursor agent run (model: `gpt-5.2-high`) with access to this repo.
  - Do NOT copy/paste the full context JSON into the prompt.
  - Instead, instruct the agent to read the context from:
    - `description_research/configurations_descriptions_step_4_context.json`
  - Provide the batch boundaries in the prompt (either:
    - a `[startKey, endKey]` range in sorted order, or
    - an explicit list of `(key, implementation)` pairs to process for this batch).
  - Allow the agent to read/search this repository to confirm details when the context packet is incomplete.
  - Require the agent to update the overrides file (append/merge entries) and keep it valid JSON at all times.
- After all batches:
  - De-duplicate overrides by `(key, implementation)` (later entries must not override earlier ones silently).
  - Sort overrides by `key`, then `implementation`.
  - Validate the file against the checklist below.

**Prompt template (copy/paste):**

System prompt (paste as-is):

```text
You are a software documentation assistant.
You write accurate, user-facing configuration descriptions.
You must follow the output format exactly.

Task:
Generate overrides for missing configuration descriptions for dd-trace-go.
You are running inside Cursor with access to the repository source code.

Input:
The keys you need to complete are are stored in this repo at:
@description_research/result/configurations_llm_needed_keys.json

Each entry describes one (key, implementation) and includes technical context extracted from code and prior pipeline steps.

Batching:
For this run, ONLY process the entries in the following batch:
10 first keys

Rules:
- Use the context packet as the primary input.
- You MAY read/search this repository to validate or complete missing details, but you must not guess. Prefer code as the source of truth.
- If you cannot confidently determine behavior, defaults, constraints, or allowed values, output NO entry for that (key, implementation).
- Do not reference “the context” or “this file” in the descriptions.
- Keep the language product-agnostic and tracer-agnostic unless the code explicitly requires specificity.
- Do not include markdown, code fences, or commentary in your output.

Output:
Update the source file @description_research/result/configurations_llm_needed_keys.json
complete the "description"

Constraints:
- description: 1–3 sentences, user-facing, explains what the setting controls and how it affects behavior.
- No duplicate (key, implementation) pairs.

Selection:
- Only produce entries for (key, implementation) pairs present in the context file.
- Only produce entries for the (key, implementation) pairs included in the batch for this run.
- If an entry already exists in the overrides file for the same (key, implementation), do not change it unless you are strictly increasing correctness (and explain via a code comment in the PR, not in the JSON).
```

This adds `results[]` entries with `source: "llm_generated"` and promotes any keys that were still missing.

## Run

The pipeline is intended to be run step-by-step: each step reads the previous step's JSON output and produces `configurations_descriptions_step_<n>.json`.

### Prerequisites

- Python 3.9+ (standard library only; no pip dependencies)
- Network access to `https://dd-feature-parity.azurewebsites.net/configurations/`
- A local checkout of `DataDog/documentation` at `description_research/documentation/` (required for step 2+)

### Step 1 — Registry documentation (`registry_doc`)

Run from the **description_research** directory (important: default paths are relative to the current working directory):

```shell
cd description_research
python3 step_1_registry_doc.py --lang golang
```

This writes:

- `./result/configurations_descriptions_step_1.json` (so: `description_research/result/configurations_descriptions_step_1.json`)

You can customize input/output locations explicitly:

```shell
cd description_research
python3 step_1_registry_doc.py \
  --lang golang \
  --supported-configurations ../internal/env/supported_configurations.json \
  --output ./result
```

Notes:

- The script logs progress to stderr; the output file contains only JSON.
- Output ordering is stable (sorted by `key`, then `implementation`) for a given registry payload and supported configurations input.
- Because Step 1 fetches a live registry endpoint, output may change over time as the registry data evolves.

### Step 2 — Documentation (same tracer language) (`documentation_same_language`)

The documentation repository we have locally is not reliably parseable with deterministic scripts (shortcodes, partials, mixed formats, etc.).
So step 2 is implemented as an **LLM-assisted extraction step**:

- a deterministic script produces an **LLM-fillable overrides JSON** listing the missing keys
- an LLM fills that file by searching the docs repo and copying the best available description text
- a deterministic script merges the filled overrides back into the step 1 output to produce the step 2 JSON

```shell
cd description_research
python3 step_2a_extract_documentation_llm_needed_keys.py --lang golang
```

This reads:

- `./result/configurations_descriptions_step_1.json`

And writes:

- `./result/configurations_descriptions_step_2_overrides.json` (LLM-fillable data file)

#### Step 2b — Fill overrides with an LLM (reviewable data)

Run the LLM **in Cursor** to fill the overrides file in-place:

- Input keys: `@description_research/result/configurations_descriptions_step_2_overrides.json`
- Docs repo: `@description_research/documentation/`
- Output (edit in place): `@description_research/result/configurations_descriptions_step_2_overrides.json`

**Prompt template (copy/paste):**

```text
You are a software documentation assistant.
You extract accurate, user-facing configuration descriptions from existing documentation.
You must follow the output format exactly.

Task:
Fill missing configuration descriptions for dd-trace-go from the Datadog documentation repo.

Inputs:
- Keys to fill: @description_research/result/configurations_descriptions_step_2_overrides.json
- Docs repo: @description_research/documentation/

Rules:
- ONLY use information you can find in the docs repo.
- Do NOT invent or speculate. If you cannot find a good description, leave "description" as "".
- Prefer copying the exact paragraph/sentence(s) from the docs; minimal cleanup is allowed (remove bullet markers, extra whitespace), but do not paraphrase.
- Quality bar: description must be self-contained, specific, and not trivially short (>= 20 characters).
- If you can, fill "sourceFile" as "path:line" pointing to where you found the description.
- Do not change keys, implementations, or metadata fields.

Batching:
- For this run, ONLY fill the first 25 entries in overrides order.
```

#### Step 2c — Materialize step 2 output (deterministic)

After the overrides file has been filled (fully or partially), merge it into step 1 output:

```shell
cd description_research
python3 step_2c_merge_documentation_llm_overrides.py --lang golang
```

This reads:

- `./result/configurations_descriptions_step_1.json`
- `./result/configurations_descriptions_step_2_overrides.json`

And writes:

- `./result/configurations_descriptions_step_2.json`
