.PHONY: build run clean

build:
	go mod tidy
	go build -o bin/coordinator ./cmd/coordinator
	go build -o bin/shard ./cmd/shard

run:
	CGO_ENABLED=0 go run ./cmd/coordinator

clean:
	rm -rf bin/
	rm -rf data/