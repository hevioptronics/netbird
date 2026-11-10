#!/bin/sh
# build.sh builds the netbird apks with abuild in an Alpine container.
#
# Run it as root from the root of a git checkout, for example:
#   docker run --rm --platform linux/amd64 -v "$PWD":/src:ro -v "$PWD/out":/out -w /src \
#     alpine@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf \
#     sh packaging/alpine/build.sh /out
# The image is alpine:3.24. The apks hold the committed tree at HEAD and are signed
# with a throwaway key generated here.
set -eu

out=$1

apk update
apk add alpine-sdk git go
adduser -D builder
addgroup builder abuild

# abuild writes next to the APKBUILD, so build from a copy the builder owns.
mkdir -p /home/builder/netbird
# The copy keeps .git: APKBUILD prepare() reads HEAD from it.
tar -cf - --exclude=./out . | tar -C /home/builder/netbird -xf -
chown -R builder:builder /home/builder/netbird

su builder -c 'abuild-keygen -a -n'
# abuild indexes the repository and needs to trust its own key for that.
cp /home/builder/.abuild/*.rsa.pub /etc/apk/keys/
su builder -c 'cd ~/netbird/packaging/alpine && REPODEST=~/packages abuild -r'

mkdir -p "$out"
find /home/builder/packages -name '*.apk' -exec cp {} "$out"/ \;
ls -l "$out"
