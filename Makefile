APP     := golm
VERSION := v0.1.0
LDFLAGS := -s -w -X github.com/leelsey/golm.Version=$(VERSION)
BUILD   := CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)'
BIN     := bin
DIST    := dist
PKG     := ./cmd/golm

PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: all build clean test test-nested release $(PLATFORMS)

all: build

build:
	$(BUILD) -o $(BIN)/$(APP) $(PKG)

test:
	go test -v ./...

# Nested modules are invisible to ./... by design; they carry the dependencies
# the main module refuses, and are built and tested on their own.
test-nested:
	@for m in $$(find . -mindepth 2 -name go.mod -not -path './.git/*'); do \
		d=$$(dirname $$m); echo "== $$d"; \
		( cd $$d && gofmt -l . && go vet ./... && go test ./... ) || exit 1; \
	done

clean:
	rm -rf $(BIN) $(DIST)

release: $(PLATFORMS)
	@cd $(DIST) && shasum -a 256 $(APP)-* > checksums.txt
	@echo "release binaries in $(DIST)/"

$(PLATFORMS):
	$(eval OS := $(word 1,$(subst /, ,$@)))
	$(eval ARCH := $(word 2,$(subst /, ,$@)))
	$(eval EXT := $(if $(filter windows,$(OS)),.exe,))
	GOOS=$(OS) GOARCH=$(ARCH) $(BUILD) -o $(DIST)/$(APP)-$(OS)-$(ARCH)$(EXT) $(PKG)
