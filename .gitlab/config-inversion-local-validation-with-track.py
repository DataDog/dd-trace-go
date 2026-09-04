#!/usr/bin/env python3
# Draft of the updated libdatadog-build config-inversion-local-validation.py.
# Adds lifecycle origin tracking on top of the existing validation.
#
# Required env vars (set by the CI template):
#   FP_API_KEY          - Feature Parity Dashboard API key (from SSM)
#   GH_TOKEN            - short-lived GitHub token (from dd-octo-sts, set in before_script)
#   CI_COMMIT_SHA       - set by GitLab CI
#   CI_COMMIT_BRANCH    - set by GitLab CI
#   CI_DEFAULT_BRANCH   - set by GitLab CI
#   CI_PROJECT_PATH     - set by GitLab CI (e.g. "DataDog/dd-trace-go")
#   LANGUAGE_NAME       - set by the job (e.g. "golang")
#   MULTIPLE_RELEASE_LINES - "true"/"false", set by the job

import argparse
import json
import os
import subprocess
import sys
import time
import requests

from config_parser import load_config_file
from config_inversion_verify_format import validate_config_data

BOLD_RED = '\033[1;31m'
BOLD_GREEN = '\033[1;32m'
BOLD_YELLOW = '\033[1;33m'
NC = '\033[0m'
BOLD = '\033[1m'

FPD_BASE_URL = 'https://dd-feature-parity.azurewebsites.net'
GITHUB_API = 'https://api.github.com'
MAX_RETRIES = 2
RETRY_BACKOFF = [2, 5]  # seconds


# ── Payload normalization (unchanged from current script) ─────────────────────

def normalize_v2_format(payload):
    if not isinstance(payload, dict):
        return payload
    normalized = {}
    for config_name, implementations in payload.items():
        normalized_implementations = []
        for item in implementations:
            copied = dict(item)
            if 'version' not in copied and 'implementation' in copied:
                copied['version'] = copied.pop('implementation')
            if 'programmaticOptions' in copied:
                options = copied.pop('programmaticOptions') or []
                if options:
                    copied['programmaticOption'] = options[0]
            normalized_implementations.append(copied)
        normalized[config_name] = normalized_implementations
    return normalized


def validate_and_normalize_config(data):
    errors = validate_config_data(data)
    if errors:
        print(f"Validation found {len(errors)} error(s):")
        for err in errors:
            print(f"  ✗ {err}")
        sys.exit(1)
    return normalize_v2_format(data.get('supportedConfigurations', {}))


# ── GitHub API: resolve PR number for the current commit ─────────────────────

def resolve_pr_number(repo, commit_sha, gh_token):
    """
    Returns the PR number whose merge_commit_sha matches the current commit,
    or None for direct pushes / pre-PR commits.
    Strategy-agnostic: works with merge, squash, and rebase.
    """
    url = f"{GITHUB_API}/repos/{repo}/commits/{commit_sha}/pulls"
    headers = {'Authorization': f'Bearer {gh_token}', 'Accept': 'application/vnd.github+json'}

    for attempt in range(1 + MAX_RETRIES):
        try:
            response = requests.get(url, headers=headers, timeout=10)
        except requests.exceptions.RequestException as e:
            if attempt < MAX_RETRIES:
                time.sleep(RETRY_BACKOFF[attempt])
                continue
            print(f"{BOLD_RED}Error: GitHub API request failed: {e}{NC}")
            sys.exit(1)

        if response.status_code in (429, 500, 502, 503, 504):
            if attempt < MAX_RETRIES:
                time.sleep(RETRY_BACKOFF[attempt])
                continue
            print(f"{BOLD_RED}Error: GitHub API returned {response.status_code} after retries{NC}")
            sys.exit(1)

        if response.status_code != 200:
            print(f"{BOLD_RED}Error: GitHub API returned {response.status_code}: {response.text}{NC}")
            sys.exit(1)

        for pr in response.json():
            if pr.get('merge_commit_sha') == commit_sha:
                return pr['number']
        return None  # no match: direct push or pre-PR commit

    return None


# ── Origin block ──────────────────────────────────────────────────────────────

def build_origin(pr_number):
    commit_sha = os.environ.get('CI_COMMIT_SHA', '')
    branch = os.environ.get('CI_COMMIT_BRANCH', '')
    is_default_branch = branch == os.environ.get('CI_DEFAULT_BRANCH', 'main')

    committed_at = subprocess.check_output(
        ['git', 'show', '-s', '--format=%cI', commit_sha], text=True
    ).strip()
    author = subprocess.check_output(
        ['git', 'show', '-s', '--format=%ae', commit_sha], text=True
    ).strip()

    origin = {
        'commit_sha': commit_sha,
        'committed_at': committed_at,
        'author': author,
        'language': os.environ.get('LANGUAGE_NAME', ''),
        'is_default_branch': is_default_branch,
    }
    if pr_number is not None:
        origin['pr_number'] = pr_number
        origin['repo'] = os.environ.get('CI_PROJECT_PATH', '')
    if os.environ.get('MULTIPLE_RELEASE_LINES', 'false').lower() == 'true':
        origin['branch'] = branch

    return origin


# ── FPD track call ────────────────────────────────────────────────────────────

def send_track_request(supported_configs, origin, fp_api_key, verbose=False):
    url = f'{FPD_BASE_URL}/configurations/validate/v2/track?backfilled=true'
    payload = {'supportedConfigurations': supported_configs, 'origin': origin}
    print(f"\n{BOLD}Payload to be sent to POST {url}:{NC}")
    print(json.dumps(payload, indent=2, default=str))
    print()
    try:
        response = requests.post(
            url,
            json=payload,
            headers={'Content-Type': 'application/json', 'FP_API_KEY': fp_api_key},
            timeout=30
        )
    except requests.exceptions.RequestException as e:
        print(f"{BOLD_RED}Error: FPD request failed: {e}{NC}")
        sys.exit(1)

    if not (200 <= response.status_code < 300):
        print(f"{BOLD_RED}Error: FPD returned {response.status_code}:{NC}\n{response.text}")
        sys.exit(1)

    body = response.json()
    if verbose:
        print(json.dumps(body, indent=2))

    for t in body.get('transitions', []):
        print(f"  lifecycle: {t.get('name')} v{t.get('version')} [{t.get('language')}] "
              f"{t.get('from_status') or 'none'} -> {t.get('to_status')} ({t.get('event')})")

    if body.get('origin_recording_error'):
        print(f"{BOLD_YELLOW}Warning: origin recording error (non-blocking): {body['origin_recording_error']}{NC}")

    if body.get('failed'):
        _print_validation_errors(body)
        return False

    print(f"{BOLD_GREEN}No validation errors found{NC}")
    return True


def _print_validation_errors(body):
    print(f"{BOLD_RED}Error: There are validation errors:{NC}")
    if body.get('missingV2'):
        print(f"{BOLD_RED}Mismatch V2 Errors:{NC}")
        print(json.dumps(body['missingV2'], indent=2))
        print("To fix: update local data to match the registry, or register a new version.")
        print("https://feature-parity.us1.prod.dog/#/configurations?viewType=configurations")
    if body.get('existingOrDuplicates'):
        print(f"\n{BOLD_RED}Existing Configuration Versions:{NC}")
        for name, details in sorted(body['existingOrDuplicates'].items()):
            print(f"  - {name} matches versions {', '.join(details.get('similarVersions', []))}")
        print("To fix: align the local version with an existing registry version, or create a new one.")
        print("https://feature-parity.us1.prod.dog/#/configurations?viewType=configurations")


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('-v', '--verbose', action='store_true')
    args, _ = parser.parse_known_args()

    fp_api_key = os.environ.get('FP_API_KEY', '')
    if not fp_api_key:
        print(f"{BOLD_RED}Error: FP_API_KEY is not set{NC}")
        sys.exit(1)

    gh_token = os.environ.get('GH_TOKEN', '')
    if not gh_token:
        print(f"{BOLD_RED}Error: GH_TOKEN is not set{NC}")
        sys.exit(1)

    try:
        data = load_config_file()
    except (FileNotFoundError, ValueError, ImportError) as e:
        print(f"Error loading local config file: {e}")
        sys.exit(1)

    supported_configs = validate_and_normalize_config(data)

    commit_sha = os.environ.get('CI_COMMIT_SHA', '')
    repo = os.environ.get('CI_PROJECT_PATH', '')
    pr_number = resolve_pr_number(repo, commit_sha, gh_token)
    print(f"PR: #{pr_number}" if pr_number else "PR: not found (direct push or pre-PR commit)")

    origin = build_origin(pr_number)
    success = send_track_request(supported_configs, origin, fp_api_key, verbose=args.verbose)
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
