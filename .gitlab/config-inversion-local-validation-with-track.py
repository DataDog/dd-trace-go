#!/usr/bin/env python3
# Draft of the updated libdatadog-build config-inversion-local-validation.py.
# Adds lifecycle origin tracking on top of the existing validation.
#
# Required env vars (set by the CI template):
#   FP_API_KEY                - Feature Parity Dashboard API key (from SSM)
#   REGISTRY_PR_NUMBER        - PR number (may be empty for direct pushes), set by resolve_origin job
#   REGISTRY_PR_REPO          - "<owner>/<repo>", set by resolve_origin job
#   REGISTRY_IS_DEFAULT_BRANCH - "true"/"false", set by resolve_origin job
#   REGISTRY_BRANCH           - branch name, set by resolve_origin job
#   REGISTRY_COMMITTED_AT     - ISO 8601 commit timestamp, set by resolve_origin job
#   REGISTRY_AUTHOR           - committer email, set by resolve_origin job
#   CI_COMMIT_SHA             - set by GitLab CI
#   LANGUAGE_NAME             - set by the job (e.g. "golang")
#   MULTIPLE_RELEASE_LINES    - "true"/"false", set by the job

import argparse
import json
import os
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
            print(f"  x {err}")
        sys.exit(1)
    return normalize_v2_format(data.get('supportedConfigurations', {}))


# ── Origin block ──────────────────────────────────────────────────────────────

def build_origin():
    commit_sha = os.environ.get('CI_COMMIT_SHA', '')
    pr_number_str = os.environ.get('REGISTRY_PR_NUMBER', '').strip()
    pr_repo = os.environ.get('REGISTRY_PR_REPO', '').strip()
    is_default_branch = os.environ.get('REGISTRY_IS_DEFAULT_BRANCH', 'false').strip().lower() == 'true'
    branch = os.environ.get('REGISTRY_BRANCH', '').strip()
    committed_at = os.environ.get('REGISTRY_COMMITTED_AT', '').strip()
    author = os.environ.get('REGISTRY_AUTHOR', '').strip()

    origin = {
        'commit_sha': commit_sha,
        'committed_at': committed_at,
        'author': author,
        'language': os.environ.get('LANGUAGE_NAME', ''),
        'is_default_branch': is_default_branch,
    }

    if pr_number_str:
        origin['pr_number'] = int(pr_number_str)
        origin['repo'] = pr_repo

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

    response = None
    for attempt in range(1 + MAX_RETRIES):
        try:
            response = requests.post(
                url,
                json=payload,
                headers={'Content-Type': 'application/json', 'FP_API_KEY': fp_api_key},
                timeout=30
            )
            break
        except requests.exceptions.RequestException as e:
            if attempt < MAX_RETRIES:
                time.sleep(RETRY_BACKOFF[attempt])
                continue
            print(f"{BOLD_RED}Error: FPD request failed: {e}{NC}")
            sys.exit(1)

    if response is None:
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
        print(f"{BOLD_YELLOW}Warning: origin recording error (non-blocking): "
              f"{body['origin_recording_error']}{NC}")

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

    pr_number_str = os.environ.get('REGISTRY_PR_NUMBER', '').strip()
    print(f"PR: #{pr_number_str}" if pr_number_str else "PR: not found (direct push or pre-PR commit)")

    try:
        data = load_config_file()
    except (FileNotFoundError, ValueError, ImportError) as e:
        print(f"Error loading local config file: {e}")
        sys.exit(1)

    supported_configs = validate_and_normalize_config(data)
    origin = build_origin()
    success = send_track_request(supported_configs, origin, fp_api_key, verbose=args.verbose)
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
