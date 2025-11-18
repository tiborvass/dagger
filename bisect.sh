#!/usr/bin/env bash

set -euo pipefail

./hack/patch
foo=foo-$RANDOM
./hack/with-dev dagger -M -c 'directory | with-new-file bar BAR | changes $(directory | with-new-file '$foo' foo) | added-paths'
docker logs dagger-engine.dev 2>&1 | grep "🐞.*$foo"
