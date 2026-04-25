# AI Workflow Instructions

## Current Project State
- The REPL in `cmd/coordinator` is fully end-to-end: insert/get/delete/query/list, with SQLite persistence per shard.
- `cmd/shard` is a real gRPC server. The coordinator can talk to it via `--shard-addrs`, or run shards in-process by default.
- Still scaffolded only: replication (R=2), failover, HTTP API.

## Safe Build and Test Commands
- Compile everything: `go build ./...`
- `make build` (also runs `go mod tidy` and produces `bin/coordinator` + `bin/shard`).
- `go test ./...` is currently just a compile check — there are no real tests yet.

## Proto / gRPC Codegen
- Schema: `internal/shardpb/shard.proto`. Generated stubs (`shard.pb.go`, `shard_grpc.pb.go`) live next to it and are committed.
- Regenerate: `make proto`. This auto-installs `protoc-gen-go` and `protoc-gen-go-grpc` into `$(go env GOPATH)/bin` if missing. `protoc` itself must already be on PATH.

## Main Runnable Entry Points
- REPL coordinator (in-process shards, default): `go run ./cmd/coordinator`
- REPL coordinator (remote shards): `go run ./cmd/coordinator --shard-addrs :9000,:9001,:9002`
- Shard gRPC server: `go run ./cmd/shard --shard-id 0 --addr :9000 --db-path ./data/shard_0.db`

## How To Run The Working Path (in-process)
1. Create a `.env` file in the repo root or export environment variables.
2. Required env var: `OPENROUTER_API_KEY`
3. Optional env vars:
   - `OPENROUTER_EMBEDDING_MODEL` default: `text-embedding-3-small`
   - `OPENROUTER_BASE_URL` default: `https://openrouter.ai/api/v1`
   - `ARTICLE_DB_NUM_SHARDS` default: `3`
   - `ARTICLE_DB_DATA_DIR` default: `./data`
   - `ARTICLE_DB_INDEX` `brute` or `hnsw` (default `hnsw`)
4. Start the REPL: `go run ./cmd/coordinator`
5. Use these commands inside the REPL:
   - `insert <url>`
   - `get <id>`
   - `delete <id>`
   - `query <k> <text>`
   - `list`
   - `help`
   - `quit`

## How To Run The Distributed Path (remote shards)
1. Set the same env vars as above (the coordinator embeds queries; shards do not need `OPENROUTER_*`).
2. Start one shard process per shard id (in separate terminals or backgrounded):
   - `go run ./cmd/shard --shard-id 0 --addr :9000 --db-path ./data/shard_0.db`
   - `go run ./cmd/shard --shard-id 1 --addr :9001 --db-path ./data/shard_1.db`
   - `go run ./cmd/shard --shard-id 2 --addr :9002 --db-path ./data/shard_2.db`
3. Start the coordinator pointing at them:
   - `go run ./cmd/coordinator --shard-addrs :9000,:9001,:9002`
4. The number of `--shard-addrs` entries must match `ARTICLE_DB_NUM_SHARDS` (default 3).

## Commands:
- `insert <url>` fetches the article, summarizes (first 3 sentences), embeds the headline+summary, and stores on the shard chosen by `hash(id) % num_shards`.
- `query <k> <text>` embeds the query, fans out to every shard in parallel, merges and dedups by article ID, returns global top-k.
- Data persists across process restarts via per-shard SQLite files in `data/`.

## SQLite Notes
- The shard/storage path uses `github.com/mattn/go-sqlite3`, so builds that include shard/storage need CGO support.
- In-process REPL mode imports SQLite transitively (the coordinator constructs `*shard.Node`s) and needs CGO.
- Remote mode keeps SQLite isolated to the shard binary; the coordinator binary itself does not need CGO when only `--shard-addrs` is used.