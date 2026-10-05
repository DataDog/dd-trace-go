SHELL:=/usr/bin/env bash

.PHONY: all
all: test-apps.yml

.PHONY: clean
clean:
	rm -rf test-apps.yml

test-apps.yml: test-apps.cue
	{ echo "# Code generated via \`make test-apps.yml\`; DO NOT EDIT."; \
	  cue export --out=yaml $< | python3 -c \
	    'import sys,re; v={m.group(1):m.group(2) for l in open("test-apps.cue") for m in [re.search(r"\"([^@\"]+@[a-f0-9]{40})\"[^/]*/+\s*(v\S+)",l)] if m}; sys.stdout.writelines(re.sub(r"(uses: )(\S+@[a-f0-9]{40})",lambda m:m.group(1)+m.group(2)+" # "+v[m.group(2)] if m.group(2) in v else m.group(),l) for l in sys.stdin)'; \
	} > $@