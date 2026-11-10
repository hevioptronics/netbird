#!/bin/sh
# build.sh builds the netbird apks with abuild in an Alpine container.
#
# Run it as root from the repository root, for example:
#   docker run --rm -v "$PWD":/src:ro -v "$PWD/out":/out -w /src alpine:3.24 \
#     sh packaging/alpine/build.sh /out
# The apks are signed with a throwaway key generated here.
set -eu

out=$1

apk update
apk add alpine-sdk go
adduser -D builder
addgroup builder abuild

# abuild writes next to the APKBUILD, so build from a copy the builder owns.
mkdir -p /home/builder/netbird
tar -cf - --exclude=./.git --exclude=./out . | tar -C /home/builder/netbird -xf -
chown -R builder:builder /home/builder/netbird

su builder -c 'abuild-keygen -a -n'
# abuild indexes the repository and needs to trust its own key for that.
cp /home/builder/.abuild/*.rsa.pub /etc/apk/keys/
su builder -c 'cd ~/netbird/packaging/alpine && REPODEST=~/packages abuild -r'

mkdir -p "$out"
find /home/builder/packages -name '*.apk' -exec cp {} "$out"/ \;
ls -l "$out"
