.PHONY: build run clean

build:
	go mod tidy
	go build -o bin/coordinator ./cmd/coordinator
	go build -o bin/shard ./cmd/shard

run:
	go run ./cmd/coordinator -shards=3 -data-dir=./data -addr=:8080

clean:
	rm -rf bin/
	rm -rf data/