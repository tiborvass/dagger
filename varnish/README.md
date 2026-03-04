# Git Varnish Cache (GitHub-first)

This folder contains a Dockerized Varnish pull-through cache for public GitHub Git HTTP traffic.

## What It Does

When the engine is started with `DAGGER_VARNISH=http://<varnish-host>:<port>`, public Git remotes like:

- `https://github.com/org/repo.git`

are rewritten by the engine to:

- `http://<varnish-host>:<port>/github.com/org/repo.git`

The Varnish config in this folder supports `github.com` only.

## Start Varnish

```bash
./varnish/run.sh
```

Defaults:

- Container name: `dagger-git-varnish`
- Volume name: `dagger-git-varnish-cache`
- Port: `6084`
- Cache backend: file storage in Docker volume (`/var/lib/varnish/cache.bin`)

Optional environment variables:

- `DAGGER_VARNISH_CONTAINER_NAME`
- `DAGGER_VARNISH_VOLUME_NAME`
- `DAGGER_VARNISH_PORT`
- `DAGGER_VARNISH_CACHE_SIZE`
- `DAGGER_VARNISH_IMAGE`

## Stop Varnish (without deleting cache)

```bash
./varnish/down.sh
```

This removes only the container. The Docker volume remains, so cache persists across restarts.

## Engine Setup

`DAGGER_VARNISH` must be present in the engine process environment at startup.

Example value:

```bash
DAGGER_VARNISH=http://host.docker.internal:6084
```

Note: with `_EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-dev-tibor`, setting `DAGGER_VARNISH` only on the CLI command does not reconfigure an already running engine container.

## Cold-start Benchmark Flow

1. Start or restart the engine with `DAGGER_VARNISH` set.
2. Prune only engine cache:

```bash
_EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-dev-tibor ./bin/dagger core engine local-cache prune
```

3. Run timed command:

```bash
time _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-dev-tibor ./bin/dagger -m github.com/dagger/dagger call --help
```

4. Repeat prune + timed run to compare:

- Run A: cold engine cache + cold/warming varnish cache
- Run B: cold engine cache + warm varnish cache

`dagger core engine local-cache prune` does not prune the Varnish Docker volume.
