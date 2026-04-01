#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

IMAGE_NAME="${CLI_PROXY_IMAGE:-cliproxyapi-pgstore-local:20260331}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.pgstore.local.yml}"
CONFIG_PATH="${CLI_PROXY_CONFIG_PATH:-./config.yaml}"
LOG_PATH="${CLI_PROXY_LOG_PATH:-./logs-pgstore-local}"
IMAGE_TAR="${IMAGE_TAR:-${IMAGE_NAME//[:\/]/-}.tar}"
ACTION="${1:-}"

compose() {
  CLI_PROXY_IMAGE="${IMAGE_NAME}" \
  CLI_PROXY_CONFIG_PATH="${CONFIG_PATH}" \
  CLI_PROXY_LOG_PATH="${LOG_PATH}" \
  docker-compose -f "${COMPOSE_FILE}" "$@"
}

load_image() {
  if [[ -f "${IMAGE_TAR}" ]]; then
    echo "Loading docker image from ${IMAGE_TAR}"
    docker load -i "${IMAGE_TAR}"
  else
    echo "Image tar not found: ${IMAGE_TAR}"
    exit 1
  fi
}

usage() {
  cat <<'EOF'
Usage:
  ./deploy-pgstore-local.sh start
  ./deploy-pgstore-local.sh restart
  ./deploy-pgstore-local.sh update
  ./deploy-pgstore-local.sh status

Actions:
  start    Load image tar and start the stack
  restart  Restart cli-proxy-api only
  update   Load image tar and recreate cli-proxy-api
  status   Show current compose status
EOF
}

case "${ACTION}" in
  start)
    load_image
    mkdir -p "${LOG_PATH}"
    compose up -d
    ;;
  restart)
    compose restart cli-proxy-api
    ;;
  update)
    load_image
    mkdir -p "${LOG_PATH}"
    compose up -d --force-recreate cli-proxy-api
    ;;
  status)
    compose ps
    ;;
  *)
    usage
    exit 1
    ;;
esac

echo "Done."
