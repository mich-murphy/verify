# Dev tools are pinned in tools/go.mod and run with `go tool`.
GOTOOL := go tool -modfile=tools/go.mod
GOFILES = $$(git ls-files --cached --others --exclude-standard --deduplicate '*.go' | while IFS= read -r file; do if [ -f "$$file" ]; then printf '%s\n' "$$file"; fi; done)

.PHONY: build fmt fmt-check lint vet tidy-check test cover deadcode vuln secrets secrets-staged actionlint check ci

build:
	go build -o bin/verify ./cmd/verify

fmt:
	$(GOTOOL) gofumpt -w .
	$(GOTOOL) goimports -w $(GOFILES)

fmt-check:
	@out="$$($(GOTOOL) gofumpt -l .)" || exit $$?; \
	imports="$$($(GOTOOL) goimports -l $(GOFILES))" || exit $$?; \
	if [ -n "$$out$$imports" ]; then echo "needs formatting (run make fmt):"; printf '%s\n' "$$out" "$$imports"; exit 1; fi

lint:
	$(GOTOOL) golangci-lint run ./...

vet:
	go vet ./...

tidy-check:
	go mod tidy -diff
	cd tools && go mod tidy -diff

test:
	go test -race -shuffle=on -count=1 ./...

# CI runs cover uncached; the pre-push hook passes TESTFLAGS= so Go's test cache
# applies (SPEC §4.5 full-tier rules).
TESTFLAGS ?= -count=1

cover:
	go test -race $(TESTFLAGS) -coverprofile=coverage.out ./...
	$(GOTOOL) go-test-coverage --config=.testcoverage.yml

deadcode:
	@out="$$($(GOTOOL) deadcode -test ./...)" || exit $$?; \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

vuln:
	$(GOTOOL) govulncheck ./...

secrets:
	$(GOTOOL) betterleaks git --no-banner .

secrets-staged:
	$(GOTOOL) betterleaks git --pre-commit --staged --no-banner --redact .

actionlint:
	$(GOTOOL) actionlint

# Local entry point: hooks via prek.
check:
	prek run --all-files --stage pre-commit
	prek run --all-files --stage pre-push

# CI entry point: same targets, no prek.
ci: fmt-check tidy-check lint secrets actionlint vet test cover deadcode vuln
