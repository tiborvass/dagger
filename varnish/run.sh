#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CONTAINER_NAME="${DAGGER_VARNISH_CONTAINER_NAME:-dagger-git-varnish}"
VOLUME_NAME="${DAGGER_VARNISH_VOLUME_NAME:-dagger-git-varnish-cache}"
PORT="${DAGGER_VARNISH_PORT:-6081}"
CACHE_SIZE="${DAGGER_VARNISH_CACHE_SIZE:-8G}"
IMAGE="${DAGGER_VARNISH_IMAGE:-varnish:stable}"
VCL_B64="$(base64 < "${ROOT_DIR}/default.vcl" | tr -d '\n')"

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
  -e "DAGGER_VARNISH_VCL_B64=${VCL_B64}" \
  "${IMAGE}" \
  sh -ec '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null
    apt-get install -y --no-install-recommends stunnel4 ca-certificates >/dev/null
    echo "$DAGGER_VARNISH_VCL_B64" | base64 -d > /etc/varnish/default.vcl
    cat > /tmp/github-stunnel.conf <<'"'"'EOF'"'"'
foreground = no
pid = /tmp/stunnel.pid
setuid = root
setgid = root

[github]
client = yes
accept = 127.0.0.1:8443
connect = github.com:443
sni = github.com
verifyChain = yes
CAfile = /etc/ssl/certs/ca-certificates.crt
EOF
    stunnel /tmp/github-stunnel.conf
    varnishd -a :6081 -f /etc/varnish/default.vcl -s "file,/var/lib/varnish/cache.bin,'"${CACHE_SIZE}"'";
    exec varnishncsa -a -F "%t %h \"%r\" %s cache=%{X-Varnish-Cache}o"
  ' >/dev/null

echo "Varnish started: http://127.0.0.1:${PORT}"
echo "Container: ${CONTAINER_NAME}"
echo "Volume: ${VOLUME_NAME}"
echo "Logs: docker logs -f ${CONTAINER_NAME}"
