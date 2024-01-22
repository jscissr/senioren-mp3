#!/bin/bash

set -e

cd "${TARGET_DIR}"

# we don't need that
rm -f etc/init.d/S02sysctl

# Disable this step because it is slow (3 seconds). However, some kernel modules for devices might not get loaded.
sed "/# coldplug modules/{ :loop n; /#/b; s/^\t/\t#/; /\\\\$/b loop }" -i etc/init.d/S10mdev

# build the gstreamer plugin cache
HOME=root qemu-arm -L . ./usr/bin/gst-play-1.0 --gst-disable-registry-fork --version

exit $?
