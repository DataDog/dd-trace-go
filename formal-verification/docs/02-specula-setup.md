# Specula Setup Guide

## What is Specula?

[Specula](https://github.com/specula-org/Specula) is a tool that synthesizes TLA+ formal specifications from Go source code. It uses a 4-step pipeline combining LLM translation with static analysis and model checking to produce verified specifications.

## Prerequisites

| Prerequisite | Version | Purpose |
|-------------|---------|---------|
| Python | 3.8+ | Specula runtime |
| Java (JRE/JDK) | 11+ | TLC model checker |
| Maven | any | Building TLC from source (optional) |
| Git | any | Cloning Specula |
| ANTHROPIC_API_KEY | — | LLM-assisted translation (Step 1) |

### macOS (Homebrew)

```bash
brew install python@3.12 openjdk@21 maven git
export JAVA_HOME="$(brew --prefix openjdk@21)"
```

### Ubuntu/Debian

```bash
sudo apt install python3 python3-pip openjdk-21-jre maven git
```

### Verify Prerequisites

```bash
python3 --version   # 3.8+
java -version       # 11+
git --version
```

## Installation

### Automated

```bash
cd formal-verification
./scripts/setup.sh
```

This script:
1. Checks all prerequisites
2. Clones Specula to `~/.local/share/specula`
3. Installs Python dependencies
4. Verifies the installation

### Manual

```bash
git clone https://github.com/specula-org/Specula.git ~/.local/share/specula
cd ~/.local/share/specula
./scripts/setup.sh  # or: pip install -r requirements.txt
```

### Check-Only Mode

To verify prerequisites without installing:

```bash
./scripts/setup.sh --check-only
```

## Configuration

### ANTHROPIC_API_KEY

Step 1 of the pipeline uses Claude for Go→TLA+ translation. Export your API key:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

You can also add this to your shell profile (`~/.zshrc`, `~/.bashrc`).

### SPECULA_DIR

By default, Specula is installed to `~/.local/share/specula`. Override with:

```bash
export SPECULA_DIR="/path/to/specula"
```

## Verification

After setup, verify the installation:

```bash
# Check Specula is accessible
~/.local/share/specula/specula --help

# Dry-run Phase 1
./scripts/run-span-lifecycle.sh --dry-run

# Dry-run Phase 2
./scripts/run-gls-context.sh --dry-run
```

## Troubleshooting

### "Python 3.8+ required"

Ensure `python3` points to a recent Python. On macOS with multiple versions:

```bash
brew link python@3.12
```

### "Java not found"

TLC requires a Java runtime. Install a JDK/JRE:

```bash
# macOS
brew install openjdk@21

# Verify
java -version
```

### "Specula entry point not found"

The Specula project may restructure its entry point. Check the repository:

```bash
ls ~/.local/share/specula/
# Look for: specula, specula.py, or a bin/ directory
```

### Step 1 Fails with API Error

- Verify `ANTHROPIC_API_KEY` is set and valid
- Check API quota at console.anthropic.com
- Try with `--step 1` to isolate the issue
