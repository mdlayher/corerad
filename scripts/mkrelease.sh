#!/usr/bin/env bash
set -e

if [[ -z $1 ]]; then
    echo "must specify a version number argument"
    exit 1
fi

if [[ $1 =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "tagging release: $1"
else
    echo "invalid version number: $1"
    exit 1
fi

TIMESTAMP=$(date +%s)
echo $1 > .gittag
echo $TIMESTAMP > .gittagtime

set -x

git commit --date $TIMESTAMP -a -s -v -S -m "corerad: tag $1"
git tag -f -s $1 -m "$1"
