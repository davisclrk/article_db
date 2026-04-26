# AI Workflow Instructions

## Current Project State
- The REPL in `cmd/coordinator` is fully end-to-end: insert/get/delete/query/list, with SQLite persistence per shard.
- `cmd/shard` is a real gRPC server. The coordinator can talk to remote shard primaries and replicas via `--primary-addrs` and `--replica-addrs`, or run shards in-process by default.
- Remote mode now does synchronous replication with strict write acknowledgment and passive read failover.
- The REPL in `cmd/coordinator` is the client interface -- there is no separate HTTP/GUI client

## Safe Build and Test Commands
- Compile everything: `go build ./...`
- `make build` (also runs `go mod tidy` and produces `bin/coordinator` + `bin/shard`).

## Proto / gRPC Codegen
- Schema: `internal/shardpb/shard.proto`. Generated stubs (`shard.pb.go`, `shard_grpc.pb.go`) live next to it and are committed.
- Regenerate: `make proto`. This auto-installs `protoc-gen-go` and `protoc-gen-go-grpc` into `$(go env GOPATH)/bin` if missing. `protoc` itself must already be on PATH.

## Main Runnable Entry Points
- REPL coordinator (remote shards): `go run ./cmd/coordinator --primary-addrs :9000,:9002,:9004 --replica-addrs :9001,:9003,:9005`
- Shard gRPC server: `go run ./cmd/shard --shard-id 0 --primary=true --addr :9000 --replica-addr :9001 --db-path ./data/shard_0_primary.db`

## How To Run The Distributed Path (remote shards)
1. Create a `.env` file in the repo root or export environment variables.
2. Required env var: `OPENROUTER_API_KEY`
3. Optional env vars:
   - `OPENROUTER_EMBEDDING_MODEL` default: `text-embedding-3-small`
   - `OPENROUTER_BASE_URL` default: `https://openrouter.ai/api/v1`
   - `ARTICLE_DB_NUM_SHARDS` default: `3`
   - `ARTICLE_DB_DATA_DIR` default: `./data`
   - `ARTICLE_DB_INDEX` `brute` or `hnsw` (default `hnsw`)
4. Start one primary and one replica shard process per logical shard:
   - `go run ./cmd/shard --shard-id 0 --primary=true --addr :9000 --replica-addr :9001 --db-path ./data/shard_0_primary.db`
   - `go run ./cmd/shard --shard-id 0 --primary=false --addr :9001 --db-path ./data/shard_0_replica.db`
   - `go run ./cmd/shard --shard-id 1 --primary=true --addr :9002 --replica-addr :9003 --db-path ./data/shard_1_primary.db`
   - `go run ./cmd/shard --shard-id 1 --primary=false --addr :9003 --db-path ./data/shard_1_replica.db`
   - `go run ./cmd/shard --shard-id 2 --primary=true --addr :9004 --replica-addr :9005 --db-path ./data/shard_2_primary.db`
   - `go run ./cmd/shard --shard-id 2 --primary=false --addr :9005 --db-path ./data/shard_2_replica.db`
5. Start the coordinator pointing at them:
   - `go run ./cmd/coordinator --primary-addrs :9000,:9002,:9004 --replica-addrs :9001,:9003,:9005`
6. The number of `--primary-addrs` and `--replica-addrs` entries must each match `ARTICLE_DB_NUM_SHARDS` (default 3).
7. The coordinator writes only to primaries. Each primary writes to its replica before success is returned. If a primary becomes unavailable, reads can fail over to the replica, but writes to that shard fail until both nodes are available again.
8. Use these commands inside the REPL:
   - `insert <url>`
   - `get <id>`
   - `delete <id>`
   - `query <k> <text>`
   - `list`
   - `help`
   - `quit`

## Commands:
- `insert <url>` fetches the article, summarizes (first 3 sentences), embeds the headline+summary, and stores on the shard chosen by `hash(id) % num_shards`.
- `query <k> <text>` embeds the query, fans out to every shard in parallel, merges and dedups by article ID, returns global top-k.
- Data persists across process restarts via per-shard SQLite files in `data/`.

## SQLite Notes
- The shard/storage path uses `github.com/mattn/go-sqlite3`, so builds that include shard/storage need CGO support.
- In-process REPL mode imports SQLite transitively (the coordinator constructs `*shard.Node`s) and needs CGO.
- Remote mode keeps SQLite isolated to the shard binary; the coordinator binary itself does not need CGO when only `--primary-addrs` and `--replica-addrs` are used.
