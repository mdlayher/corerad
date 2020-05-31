#!/bin/bash
set -x
set -e

cd internal/corerad
while inotifywait -e close_write *.go; do go test -race -cover -c -o ./corerad.test && sudo ./corerad.test -test.coverprofile cover.out ./...; done
cd -
