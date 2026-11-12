.PHONY: build test race vet bench fmt check vuln
build:
	GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath -o bin/agent-memory ./cmd/agent-memory
test:
	GOTOOLCHAIN=local go test ./...
race:
	GOTOOLCHAIN=local go test -race ./...
vet:
	GOTOOLCHAIN=local go vet ./...
bench:
	GOTOOLCHAIN=local go test ./internal/memory -run '^$$' -bench BenchmarkWorkloads -benchtime=100x -benchmem
fmt:
	gofmt -w cmd internal
check: test race vet build
# Needs govulncheck on PATH and network access to vuln.go.dev, so it is not part of check.
vuln:
	GOTOOLCHAIN=local govulncheck ./...
