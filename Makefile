.PHONY: build test lint fmt vet sec vulncheck loc check run

build:
	go build -o spoor ./cmd/spoor

test:
	go test ./... -race

fmt:
	@out=$$(gofmt -l .); test -z "$$out" || { echo "$$out"; exit 1; }

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# gosec v2.29.0 cannot read Go 1.27.2 export data (securego/gosec#1771):
# it analyses the code with Go 1.27.1 until v2.29.1 ships.
sec:
	GOTOOLCHAIN=go1.27.1 gosec ./...

vulncheck:
	govulncheck ./...

# The line budget (AGENTS.md): non-test Go + templates + schema
# migrations must stay readable in one afternoon.
LOC_BUDGET := 10000
loc:
	@n=$$(find cmd internal migrations \( -name '*.go' -o -name '*.html' -o -name '*.sql' \) \
		! -name '*_test.go' ! -path '*/storetest/*' | xargs cat | wc -l); \
	echo "loc: $$n / $(LOC_BUDGET)"; test $$n -le $(LOC_BUDGET)

check: fmt vet lint sec vulncheck loc test

run: build
	./spoor serve
