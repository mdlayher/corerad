#!/bin/bash
set -x
set -e

cd internal/corerad
while inotifywait -e close_write *.go; do go test -race -cover -c -mod vendor -o ./corerad.test && sudo ./corerad.test -test.v -test.coverprofile cover.out ./...; done
cd -
