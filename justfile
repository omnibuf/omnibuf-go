# List available recipes
default:
    @just --list

# ==============================================================================
# Go Directives
# ==============================================================================

# Run all Go unit, Protobuf bridge, plugin, conformance, and competitor tests
test:
    go test -race -v ./...
    cd omnibufproto && go test -race -v ./...
    cd cmd/protoc-gen-omnibuf-go && go test -race -v ./...
    cd benchmarks && go test -race -v ./...

# Build and run the conformance test runner binary against an adapter binary (defaults to Go adapter ./bin/conformance-go)
conformance adapter="./bin/conformance-go": build-conformance
    ./bin/conformance-runner "{{adapter}}"

# Build both the conformance runner and the Go conformance adapter binaries into ./bin/
build-conformance: build-conformance-runner build-conformance-go

# Build the language-independent conformance test runner binary into ./bin/conformance-runner
build-conformance-runner:
    @mkdir -p bin
    go build -o bin/conformance-runner ./cmd/conformance-runner

# Build the Go conformance adapter binary into ./bin/conformance-go
build-conformance-go:
    @mkdir -p bin
    cd omnibufproto && go build -o ../bin/conformance-go ./cmd/conformance-go

# Regenerate golden files in testdata/golden/ and testdata/conformance_corpus.json
update-golden:
    cd omnibufproto && go test -v -run "TestGoldenWireFiles|TestProtobufStyleConformanceCorpus" . -update-golden

# Run native Go fuzzers against both the zero-dep core decoder and full Protobuf conformance tiers
fuzz time="10s":
    go test -fuzz=FuzzCoreDecodeNoPanic -fuzztime={{time}} .
    cd omnibufproto && go test -fuzz=FuzzConformanceNoPanic -fuzztime={{time}} .

# Check compiler inlining for hot paths, then lint all Go modules using golangci-lint
go-lint: go-inline-check
    golangci-lint run ./...
    cd omnibufproto && golangci-lint run ./...
    cd cmd/protoc-gen-omnibuf-go && golangci-lint run ./...
    cd benchmarks && golangci-lint run ./...

# Fail if a performance-critical function loses compiler inlining eligibility
go-inline-check:
    go run ./internal/cmd/checkinline

# Format Go code across all modules using golangci-lint fmt
go-fmt:
    golangci-lint fmt
    cd omnibufproto && golangci-lint fmt
    cd cmd/protoc-gen-omnibuf-go && golangci-lint fmt
    cd benchmarks && golangci-lint fmt

# Check Go code formatting across all modules
go-fmt-check:
    golangci-lint fmt --diff
    cd omnibufproto && golangci-lint fmt --diff
    cd cmd/protoc-gen-omnibuf-go && golangci-lint fmt --diff
    cd benchmarks && golangci-lint fmt --diff

# Run all Go benchmarks with memory allocation stats and persist output to testdata/benchmarks.txt and testdata/benchmarks.json
bench:
    cd benchmarks && GOEXPERIMENT=jsonv2 go test -bench=. -benchmem . | tee ../testdata/benchmarks.txt
    go run ./internal/cmd/exportbench

# Parse testdata/benchmarks.txt into structured testdata/benchmarks.json
export-bench:
    go run ./internal/cmd/exportbench

# Regenerate omnibufproto/internal/pb and benchmarks/pb generated files
generate:
    cd cmd/protoc-gen-omnibuf-go && go run . -self-gen=../../omnibufproto/internal/pb/peer_omnibuf.pb.go
    cd cmd/protoc-gen-omnibuf-go && go run . -conformance-gen=../../omnibufproto/internal/pb/conformance_omnibuf.pb.go
    cp omnibufproto/internal/pb/peer_omnibuf.pb.go benchmarks/pb/peer_omnibuf.pb.go
    cd benchmarks && go run ./cmd/vtprotogen -out=pb/peer_vtproto.pb.go

# Build the protoc-gen-omnibuf-go plugin binary into ./bin/protoc-gen-omnibuf-go
build-plugin:
    @mkdir -p bin
    cd cmd/protoc-gen-omnibuf-go && go build -o ../../bin/protoc-gen-omnibuf-go .

# ==============================================================================
# Protobuf / Buf Directives
# ==============================================================================

# Lint Protobuf schemas using buf
buf-lint:
    buf lint

# Format Protobuf schemas in place using buf
buf-fmt:
    buf format -w

# Check Protobuf schema formatting
buf-fmt-check:
    buf format -d --exit-code

# Verify all Protobuf schemas compile cleanly using buf
buf-build:
    buf build

# Check Protobuf schemas for breaking changes against a git ref (default: main branch)
buf-breaking against=".git#branch=main":
    buf breaking --against "{{against}}"

# ==============================================================================
# Combined Repo-Wide Directives (Format, Lint, CI)
# ==============================================================================

# Format Go and Protobuf code
fmt: go-fmt buf-fmt

# Check formatting across Go and Protobuf code
fmt-check: go-fmt-check buf-fmt-check

# Lint Go and Protobuf code
lint: go-lint buf-lint

# Run all Go CI checks (format check, lint, unit tests, conformance runner + adapter, and fuzzing)
ci-go fuzz_time="5s": go-fmt-check go-lint test conformance (fuzz fuzz_time)

# Run all Protobuf CI checks (format check, lint, and build)
ci-proto: buf-fmt-check buf-lint buf-build

# Run full CI suite across Go and Protobuf
ci fuzz_time="5s": (ci-go fuzz_time) ci-proto
