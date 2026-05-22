# stalewood — task runner (https://github.com/casey/just)

binary := "stalewood"

# list available recipes
default:
    @just --list

# build the binary
build:
    go build -o {{binary}} .

# run the test suite
test:
    go test ./...

# run the test suite verbosely
test-v:
    go test -v ./...

# format all Go sources in place
fmt:
    gofmt -w .

# fail if any Go source is unformatted (non-mutating, for CI)
fmt-check:
    @unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "unformatted: $unformatted"; exit 1; fi

# run go vet
vet:
    go vet ./...

# tidy go.mod / go.sum
tidy:
    go mod tidy

# fmt-check + vet + test — run this before committing
check: fmt-check vet test

# run stalewood (pass flags/path as args, e.g. `just run -size ~/projects`)
run *args:
    go run . {{args}}

# install the binary to GOBIN (~/go/bin by default)
install:
    go install .

# build a local release snapshot with goreleaser (needs goreleaser)
dist:
    goreleaser release --snapshot --clean

# remove build artifacts
clean:
    rm -f {{binary}}

# cut a release: writes VERSION, commits and tags vX.Y.Z (e.g. just release 0.1.5)
release version:
    printf '%s\n' '{{version}}' > VERSION
    git add VERSION
    git commit -m 'Release v{{version}}'
    git tag -a 'v{{version}}' -m 'stalewood v{{version}}'
    @echo 'tagged v{{version}} - push with: git push origin main v{{version}}'
