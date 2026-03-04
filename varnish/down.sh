#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="${DAGGER_VARNISH_CONTAINER_NAME:-dagger-git-varnish}"

if docker ps -a --format '{{.Names}}' | grep -Fxq "${CONTAINER_NAME}"; then
  docker rm -f "${CONTAINER_NAME}" >/dev/null
  echo "Removed container: ${CONTAINER_NAME}"
else
  echo "Container not found: ${CONTAINER_NAME}"
fi

echo "Docker volume was left intact."
