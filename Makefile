.PHONY: all test test-race cover lint vulncheck verify tidy build decouple couple workspace-init help

all: verify lint test-race vulncheck build

test:
	go test -v ./...

test-race:
	go test -v -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

verify:
	go mod verify

tidy:
	go mod tidy

build:
	go build -v ./examples/invoice_service

# Decouple go.mod by dropping the local replace directive for open-source distribution/release
decouple:
	go mod edit -dropreplace github.com/umesh0492/go-libs
	@echo "Dropped replace directive in go.mod for standalone distribution."

# Couple go.mod for local companion workspace development alongside ../go-libs
couple:
	go mod edit -replace github.com/umesh0492/go-libs=../go-libs
	@echo "Configured replace directive in go.mod pointing to ../go-libs."

# Initialize multi-module Go workspace at parent directory without needing replace in go.mod
workspace-init:
	@cd .. && (go work init ./go-app-kit ./go-libs 2>/dev/null || go work use ./go-app-kit ./go-libs)
	@echo "Multi-module Go workspace configured in parent directory."
