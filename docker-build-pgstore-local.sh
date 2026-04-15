#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

IMAGE_NAME="${CLI_PROXY_IMAGE:-cliproxyapi-pgstore-local:20260331}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.pgstore.local.yml}"
PROXY_URL="${PROXY_URL:-http://127.0.0.1:7890}"
VERSION="${VERSION:-dev-pgstore-local}"
COMMIT="${COMMIT:-local}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

echo "Building image: ${IMAGE_NAME}"
echo "Compose file: ${COMPOSE_FILE}"
echo "Build proxy: ${PROXY_URL}"
echo "Build date: ${BUILD_DATE}"

docker build \
  --network host \
  -t "${IMAGE_NAME}" \
  --build-arg HTTP_PROXY="${PROXY_URL}" \
  --build-arg HTTPS_PROXY="${PROXY_URL}" \
  --build-arg ALL_PROXY="${PROXY_URL}" \
  --build-arg http_proxy="${PROXY_URL}" \
  --build-arg https_proxy="${PROXY_URL}" \
  --build-arg all_proxy="${PROXY_URL}" \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  -f Dockerfile \
  .

export CLI_PROXY_IMAGE="${IMAGE_NAME}"

echo "Restarting pgstore local stack..."
docker-compose -f "${COMPOSE_FILE}" up -d --no-deps --force-recreate cli-proxy-api

echo "Done."
echo "API: http://127.0.0.1:18327"
echo "Management: http://127.0.0.1:18327/management.html"
