# Needed for shell expansion
SHELL = /bin/bash

PROJECT = lxd-backup-server
PACKAGE = obmondo-lxd-backup-server

TAG := $(shell git describe --tags --match 'v[0-9]*' --abbrev=0  | tail -c+2)
GIT_EMAIL:=$(shell sed -r 's/^ *?([^ ]+)/\1/' <<< "${EMAIL} $(shell git config user.email)")

VERSION = $(shell git describe --tags | tail -c +2)
export GOPATH := $(GOPATH):$(shell pwd)/tmp/
SOURCES = $(shell find . -name '*.go' -not -name '.*' -not -path './vendor/*' -not -path './deps/*')
EXEC_FILE = $(PROJECT)
# statically link and strip binaries
LDFLAGS = '-s'

DEBUILD_DPKG_BUILDPACKAGE_OPTS := --build=binary

export DEBUILD_DPKG_BUILDPACKAGE_OPTS
export GOPATH

all: $(EXEC_FILE)

help:
	@echo "make deb      - Generate a deb package"

package: deb

.PHONY: patch minor major
patch minor major:
	@{ \
		set -e ;\
		NEW_VERSION=$(shell semver bump $@ $(TAG)) ;\
		gbp dch --debian-tag='v%(version)s' --new-version "$${NEW_VERSION}" ;\
		git add debian/changelog ;\
		echo "Release $(PROJECT) $(VERSION)" > .git/COMMIT_EDITMSG ;\
		echo 'Remember to check changelog, commit and tag!' ;\
	}

$(EXEC_FILE): $(SOURCES)
# because glide is weird and installs packages without a src dir...
	@mkdir -p tmp
	@ln -sf $(shell pwd)/vendor $(shell pwd)/tmp/src
	time go build -o "$(EXEC_FILE)" -ldflags "$(LDFLAGS)"

.PHONY: deps
deps: vendor
	glide install

deb: $(EXEC_FILE)
	@mkdir -p dist
	debuild -us -uc -b
	@mv -t dist/ $$(sed -r 's@^([^ ]+).*@../\1@' < debian/files | tr '\n' ' ')
	@dh_clean
