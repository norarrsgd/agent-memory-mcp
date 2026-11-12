.PHONY: build test race vet bench fmt check
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
