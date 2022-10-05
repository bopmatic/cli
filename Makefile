export GO111MODULE=on
export GOFLAGS=-mod=vendor

.PHONY: build
build: vendor version.txt help.txt FORCE
	go build -o bopmatic

.PHONY: test
test: build FORCE
	go test

unit-tests.xml: FORCE
	gotestsum --junitfile unit-tests.xml

vendor: go.mod
	go mod download
	go mod vendor

version.txt:
	git describe --tags > version.txt

.PHONY: clean
clean:
	rm -rf bopmatic unit-tests.xml

.PHONY: deps
deps:
	rm -rf go.mod go.sum vendor
	go mod init github.com/bopmatic/cli
	GOPROXY=direct go mod tidy
	go mod vendor

FORCE:
