#!/bin/bash

set -e

grep -qE 'boot_delay=0' "${BINARIES_DIR}/rpi-firmware/config.txt" || \
cat << __EOF__ >> "${BINARIES_DIR}/rpi-firmware/config.txt"

boot_delay=0
disable_splash=1
dtparam=audio=on
__EOF__

# Remove serial port
sed "s/ console=ttyAMA0,115200//" -i "${BINARIES_DIR}/rpi-firmware/cmdline.txt"


BOARD_DIR="$(dirname $0)"
BOARD_NAME="raspberrypi"
GENIMAGE_CFG="${BOARD_DIR}/genimage-${BOARD_NAME}.cfg"
GENIMAGE_TMP="${BUILD_DIR}/genimage.tmp"

rm -rf "${GENIMAGE_TMP}"

genimage                           \
	--rootpath "${TARGET_DIR}"     \
	--tmppath "${GENIMAGE_TMP}"    \
	--inputpath "${BINARIES_DIR}"  \
	--outputpath "${BINARIES_DIR}" \
	--config "${GENIMAGE_CFG}"

exit $?
