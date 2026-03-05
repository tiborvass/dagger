#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CONTAINER_NAME="${DAGGER_VARNISH_CONTAINER_NAME:-dagger-git-varnish}"
VOLUME_NAME="${DAGGER_VARNISH_VOLUME_NAME:-dagger-git-varnish-cache}"
PORT="${DAGGER_VARNISH_PORT:-6081}"
CACHE_SIZE="${DAGGER_VARNISH_CACHE_SIZE:-8G}"
IMAGE="${DAGGER_VARNISH_IMAGE:-varnish:stable}"

if docker ps -a --format '{{.Names}}' | grep -Fxq "${CONTAINER_NAME}"; then
  docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

if ! docker volume inspect "${VOLUME_NAME}" >/dev/null 2>&1; then
  docker volume create "${VOLUME_NAME}" >/dev/null
fi

docker run -d \
  --name "${CONTAINER_NAME}" \
  -p "${PORT}:6081" \
  -v "${VOLUME_NAME}:/var/lib/varnish" \
  -v "${ROOT_DIR}/default.vcl:/etc/varnish/default.vcl:ro" \
  "${IMAGE}" \
  varnishd -F -a :6081 -f /etc/varnish/default.vcl -s "file,/var/lib/varnish/cache.bin,${CACHE_SIZE}" >/dev/null

echo "Varnish started: http://127.0.0.1:${PORT}"
echo "Container: ${CONTAINER_NAME}"
echo "Volume: ${VOLUME_NAME}"
