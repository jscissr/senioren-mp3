#!/bin/sh

# Set snd-bcm2835 audio to analog output
amixer cset numid=3 1
# Slightly reduce volume, because otherwise there were slight artefacts on loud sounds
amixer cset numid=1 :-20

export HOME=/root
export GST_REGISTRY_UPDATE=no
go-mp3player > /dev/tty1 2>&1 &
