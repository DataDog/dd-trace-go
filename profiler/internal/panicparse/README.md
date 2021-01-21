This directory contains a fork of https://github.com/maruel/panicparse. Only the internal import paths have been updated, no other modifications to the code have been made.

The reason for the fork is that the project is incompatible with go tools such as `dep` that are unware of how go modules handle major versions. An [issue](https://github.com/maruel/panicparse/issues/57) has been raised to see if upstream is willing to change that, but we might decide to rewrite/fork anyway in the long run to avoid taking on an external dependency that does much more than we need.
