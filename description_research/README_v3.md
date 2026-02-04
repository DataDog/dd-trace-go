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

### Quality bar (steps 1–2)

Steps 1–2 are *extraction steps*; they should reject low-quality text. A description is considered usable if:

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

### 2 - Documentation extract

Label: `documentation_same_language`

This step attempts to find descriptions in **existing Datadog documentation** for the *same tracer language* as `--lang`.

This is an *extraction step*:
- Do **not** invent or paraphrase.
- The prompt should tell the AI what do to and which file to manipulate.

#### Documentation repo remarks

The Datadog documentation is example based. Parsing the documentation with a parser is not ideal as it retrieves example sentences that do not really describe the key
but rather a usecase shown.

There is also no generic structure of documentation that we can easily parse with a script to get decent results.

Instead of parsing the best thing would be to ask the LLM to extract description when it finds one. It should not invent anything but simply extract data when
found.

#### Inputs

- The previous step output `configurations_descriptions_step_1.json`
- A local checkout of the Datadog documentation repository (or the ability to clone it): `https://github.com/DataDog/documentation`.

#### What the AI should do

Generate a **step 2 script** which reads step 1 output and produces `configurations_descriptions_step_2_keys_to_search.json`
- This output file will then be filled by an LLM.

Because LLM calls are inherently non-deterministic, step 4 is split into:

- a deterministic **context extraction** script (build inputs for the LLM)
- a reviewable **overrides** file produced by the LLM (data, not code)
- a deterministic **merge** script that merges overrides into the final step JSON



### 3 - Code parser

Label: `code_context` (context packet; not a description source)

This step is a **deterministic code-context extractor** used to prepare LLM generation later.
It must **not** generate descriptions.

#### Inputs

- The previous step output `configurations_descriptions_step_2.json`
- This repository checkout (for code scanning)
- `internal/env/supported_configurations.json` (type/default/aliases)

#### Output

- `./result/configurations_descriptions_step_3_context.json` (deterministic JSON)

#### What it extracts

For each `(key, implementation)` that would still need LLM help after Step 2 (missing entries plus documented entries without a `registry_doc` result), gather:

- supported metadata (type/default/aliases) from `internal/env/supported_configurations.json`
- documentation candidates already found in Steps 1–2 (for grounding)
- bounded code references from this repo:
  - where the key is read (`env.Get`, `env.Lookup`, `stableconfig.*`, `internal.*Env`, etc.)
  - nearby doc comments and parsing/default behavior (as snippets)

The context packet should be stable-sorted by `key`, then `implementation`, with bounded/snippet-limited occurrences so it remains reviewable.

