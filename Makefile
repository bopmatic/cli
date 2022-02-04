export GO111MODULE=on
export GOFLAGS=-mod=vendor

.PHONY: build
build: vendor version.txt help.txt FORCE
	go build -o bopmatic

.PHONY: test
test: build FORCE
	go test

go.sum vendor: go.mod
	go mod vendor

version.txt:
	git describe --tags > version.txt

.PHONY: clean
clean:
	rm -rf go.sum vendor bopmatic version.txt

FORCE:
