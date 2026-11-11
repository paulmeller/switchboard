GOIMPORTS_VERSION := v0.38.0
GOFUMPT_VERSION := v0.9.2
TOOLS := $(CURDIR)/.tools
XDG_CACHE_HOME ?= $(TOOLS)/cache
export XDG_CACHE_HOME

.PHONY: deps tools fmt fmt-check vet test build ci darwin
deps:
	go mod download

tools: $(TOOLS)/goimports $(TOOLS)/gofumpt

$(TOOLS)/goimports: Makefile
	GOBIN=$(TOOLS) go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

$(TOOLS)/gofumpt: Makefile
	GOBIN=$(TOOLS) go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
fmt: tools
	$(TOOLS)/goimports -w cmd internal
	$(TOOLS)/gofumpt -w cmd internal
fmt-check: tools
	@test -z "$$($(TOOLS)/goimports -l cmd internal)" || (echo 'Run make fmt'; exit 1)
	@test -z "$$($(TOOLS)/gofumpt -l cmd internal)" || (echo 'Run make fmt'; exit 1)
vet:
	go vet ./...
test:
	go test ./...
build:
	go build -o build/sb ./cmd/sb
darwin:
	GOOS=darwin GOARCH=arm64 go build -o build/sb-darwin-arm64 ./cmd/sb
ci: deps fmt-check vet test build
