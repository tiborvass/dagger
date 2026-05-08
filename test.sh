#!/usr/bin/env bash

sed 's/Optionally install a Dagger SDK/!!!FOOBARBAZ!!!_CACHEHIT_TEST/' cmd/dagger/module.go > tmp
mv tmp cmd/dagger/module.go

#docker rm -vf dagger-engine-v0.20.8-tibor

#export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-image://registry.dagger.io/engine:v0.20.8?container=dagger-engine-v0.20.8-tibor

#/tmp/v0.20.8/
dagger generate docs:references

git checkout cmd/dagger/module.go docs/current_docs/reference/cli/index.mdx
sed 's/Optionally install a Dagger SDK/FOOBARBAZ_CACHEHIT_TEST/' cmd/dagger/module.go > tmp
mv tmp cmd/dagger/module.go

#/tmp/v0.20.8/
dagger generate docs:references -y
grep -E '!!!FOOBARBAZ' docs/current_docs/reference/cli/index.mdx
x=$?
git checkout cmd/dagger/module.go docs/current_docs/reference/cli/index.mdx
exit $(($x == 0))
