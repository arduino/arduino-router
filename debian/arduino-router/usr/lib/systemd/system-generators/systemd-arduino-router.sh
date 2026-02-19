#!/bin/sh

TARGET_DIR="$1/arduino-router.service.d/"
CONF_DIR="/var/lib/arduino-router/config"
MODEL_FILE1="/sys/class/dmi/id/product_name"
MODEL_FILE2="/sys/firmware/devicetree/base/compatible"

if [ -f "$MODEL_FILE1" ] ; then
  model=$(cat $MODEL_FILE1 | { read -r line; echo -n "$line"; })
elif [ -f "$MODEL_FILE2" ] ; then
  model=$(cat $MODEL_FILE2 | tr '\0' ',' | cut -d',' -f2)
fi

mkdir -p "$TARGET_DIR"

case "$model" in
  "Imola"*|"imola"*)
    cp "${CONF_DIR}/10-imola.conf" "$TARGET_DIR" ;;
  "Monza"*|"monza"*)
    cp "${CONF_DIR}/10-monza.conf" "$TARGET_DIR" ;;
esac
