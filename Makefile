# Thanks, CoreDNS team!
VERSION := $(shell git describe --dirty --always)
TIMESTAMP := $(shell date +%s)
CGO_ENABLED := 1
BUILDPKG := github.com/mdlayher/corerad/internal/build

all:
	CGO_ENABLED=$(CGO_ENABLED) \
	go build \
		-ldflags=" \
			-X $(BUILDPKG).linkTimestamp=$(TIMESTAMP) \
			-X $(BUILDPKG).linkVersion=$(VERSION) \
		" \
	-o ./cmd/corerad/corerad \
	./cmd/corerad

nocgo: VERSION := $(VERSION)-no-cgo
nocgo: CGO_ENABLED := 0
nocgo: all
