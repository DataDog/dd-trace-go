SHELL:=/usr/bin/env bash

.PHONY: all
all: test-apps.yml

.PHONY: clean
clean:
	rm -rf test-apps.yml

test-apps.yml: test-apps.cue
	cat <(echo "# Code generated via \`make test-apps.yml\`; DO NOT EDIT.") <(cue export --out=yaml $<) > $@

unit-integration-tests.yml: unit-integration-tests.cue
	cat <(echo "# Code generated via \`make unit-integration-tests.yml\`; DO NOT EDIT.") <(cue export services.cue go-versions.cue --out=yaml $<) > $@

pull-request.yml: pull-request.cue
	cat <(echo "# Code generated via \`make pull-request.yml\`; DO NOT EDIT.") <(cue export services.cue go-versions.cue --out=yaml $<) > $@
