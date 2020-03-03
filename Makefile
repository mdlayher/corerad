# Thanks, CoreDNS team!
VERSION := $(shell git describe --dirty --always)
TIMESTAMP := $(shell date +%s)
BUILDPKG := github.com/mdlayher/corerad/internal/build

make:
	go build \
		-mod=vendor \
		-ldflags=" \
			-X $(BUILDPKG).linkTimestamp=$(TIMESTAMP) \
			-X $(BUILDPKG).linkVersion=$(VERSION) \
		" \
	./cmd/corerad
