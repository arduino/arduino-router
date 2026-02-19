#!/bin/sh

TARGET_DIR="$1/arduino-router.service.d/"
CONF_DIR="/var/lib/arduino-router/config"
MODEL_FILE="/sys/class/dmi/id/product_name"

model=$(cat ${MODEL_FILE} | { read -r line; echo -n "$line"; })

mkdir -p "$TARGET_DIR"

case "$model" in
  "Imola"*)
    cp "${CONF_DIR}/10-imola.conf" "$TARGET_DIR" ;;
esac
