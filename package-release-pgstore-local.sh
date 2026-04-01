#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

IMAGE_NAME="${CLI_PROXY_IMAGE:-cliproxyapi-pgstore-local:20260331}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.pgstore.local.yml}"
CONFIG_FILE="${CONFIG_FILE:-config.yaml}"
LOCAL_CONFIG_FILE="${LOCAL_CONFIG_FILE:-config.pgstore.local.yaml}"
BUILD_SCRIPT="${BUILD_SCRIPT:-docker-build-pgstore-local.sh}"
DEPLOY_SCRIPT="${DEPLOY_SCRIPT:-deploy-pgstore-local.sh}"
LOG_DIR_NAME="${LOG_DIR_NAME:-logs-pgstore-local}"
RELEASE_ROOT="${RELEASE_ROOT:-release}"
RELEASE_NAME="${RELEASE_NAME:-pgstore-local}"
RELEASE_DIR="${RELEASE_ROOT}/${RELEASE_NAME}"
ARCHIVE_PATH="${RELEASE_ROOT}/${RELEASE_NAME}.tar.gz"
IMAGE_TAR_NAME="${IMAGE_NAME//[:\/]/-}.tar"

mkdir -p "${RELEASE_ROOT}"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  echo "Running build script: ${BUILD_SCRIPT}"
  "./${BUILD_SCRIPT}"
else
  echo "Skipping build step because SKIP_BUILD=1"
fi

echo "Preparing release directory: ${RELEASE_DIR}"
rm -rf "${RELEASE_DIR}"
mkdir -p "${RELEASE_DIR}/${LOG_DIR_NAME}"

cp -f "${BUILD_SCRIPT}" "${RELEASE_DIR}/"
cp -f "${DEPLOY_SCRIPT}" "${RELEASE_DIR}/"
cp -f "${COMPOSE_FILE}" "${RELEASE_DIR}/"
cp -f "${CONFIG_FILE}" "${RELEASE_DIR}/"

if [[ -f "${LOCAL_CONFIG_FILE}" ]]; then
  cp -f "${LOCAL_CONFIG_FILE}" "${RELEASE_DIR}/"
fi

cp -f "$0" "${RELEASE_DIR}/"

echo "Saving docker image to ${RELEASE_DIR}/${IMAGE_TAR_NAME}"
docker save -o "${RELEASE_DIR}/${IMAGE_TAR_NAME}" "${IMAGE_NAME}"

if [[ -d "${LOG_DIR_NAME}" ]]; then
  echo "Copying readable log files from ${LOG_DIR_NAME}"
  while IFS= read -r -d '' file; do
    cp -f "${file}" "${RELEASE_DIR}/${LOG_DIR_NAME}/"
  done < <(find "${LOG_DIR_NAME}" -maxdepth 1 -type f -readable -print0)
fi

touch "${RELEASE_DIR}/${LOG_DIR_NAME}/.gitkeep"

printf 'image=%s\ncompose=%s\nconfig=%s\nbuild_script=%s\ndeploy_script=%s\npackaged_at=%s\n' \
  "${IMAGE_NAME}" \
  "${COMPOSE_FILE}" \
  "${CONFIG_FILE}" \
  "${BUILD_SCRIPT}" \
  "${DEPLOY_SCRIPT}" \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "${RELEASE_DIR}/RELEASE_INFO.txt"

echo "Creating archive: ${ARCHIVE_PATH}"
rm -f "${ARCHIVE_PATH}"
tar -czf "${ARCHIVE_PATH}" -C "${RELEASE_ROOT}" "${RELEASE_NAME}"

echo "Release ready:"
echo "  Directory: ${RELEASE_DIR}"
echo "  Archive: ${ARCHIVE_PATH}"
