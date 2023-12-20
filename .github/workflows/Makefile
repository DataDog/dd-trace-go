SHELL:=/usr/bin/env bash

.PHONY: all
all: test-apps.yml

.PHONY: clean
clean:
	rm -rf test-apps.yml

test-apps.yml: test-apps.cue
	cat <(echo "# Code generated via \`make test-apps.yml\`; DO NOT EDIT.") <(cue export --out=yaml $<) > $@