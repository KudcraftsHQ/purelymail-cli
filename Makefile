SPEC_DIR  ?= .spec
SPEC       = $(SPEC_DIR)/purelymail-openapi.json
SPEC_URL   = https://news.purelymail.com/api/swagger-spec.js
SCHEMA     = internal/tree/schema.json
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    = -s -w -X github.com/KudcraftsHQ/purelymail-cli/internal/dispatch.Version=$(VERSION)

.PHONY: build test vet fmt lint tree spec clean install snapshot

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/purelymail ./cmd/purelymail

install:
	CGO_ENABLED=0 go install -ldflags '$(LDFLAGS)' ./cmd/purelymail

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: fmt vet test

# Download Purelymail's OpenAPI spec. The docs page loads it as a script that
# assigns `window.swaggerSpec = {...}`; stripping the assignment leaves JSON.
# The spec is Purelymail's and is not committed -- only the schema derived from it.
spec:
	mkdir -p $(SPEC_DIR)
	curl -fsSL $(SPEC_URL) \
	  | sed -e '1s/^[[:space:]]*window\.swaggerSpec[[:space:]]*=[[:space:]]*//' -e '$$s/;[[:space:]]*$$//' \
	  > $(SPEC)
	python3 -c 'import json,sys; json.load(open(sys.argv[1]))' $(SPEC)

# Regenerate the command tree from the spec. The schema is committed, so this
# only runs when Purelymail changes its API -- nobody needs Python otherwise.
tree: spec
	python3 tools/gen_command_tree.py --spec $(SPEC) --out $(SCHEMA)

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist $(SPEC_DIR)
