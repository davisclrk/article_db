.PHONY: build run clean proto proto-tools

GOPATH ?= $(shell go env GOPATH)
PROTOC_GEN_GO := $(GOPATH)/bin/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(GOPATH)/bin/protoc-gen-go-grpc

build:
	go mod tidy
	go build -o bin/coordinator ./cmd/coordinator
	go build -o bin/shard ./cmd/shard

run:
	CGO_ENABLED=0 go run ./cmd/coordinator

clean:
	rm -rf bin/
	rm -rf data/

proto: proto-tools
	PATH="$(GOPATH)/bin:$$PATH" protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		internal/shardpb/shard.proto

proto-tools: $(PROTOC_GEN_GO) $(PROTOC_GEN_GO_GRPC)

$(PROTOC_GEN_GO):
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

$(PROTOC_GEN_GO_GRPC):
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest