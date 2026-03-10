# AI Workflow Instructions

## Current Project State
- The only fully usable end-to-end flow currently is the local REPL in `cmd/coordinator`.
- The distributed HTTP/coordinator/shard/SQLite architecture is scaffolded, but the core coordinator CRUD/query methods are still being developed.

## Safe Build and Test Commands
- Compile everything: `go build ./...`
- `go test ./...` currently acts as a compile check as there are no real test files in the tree right now.

## Main Runnable Entry Points
- Local REPL coordinator: `go run ./cmd/coordinator`
- Placeholder shard process: `go run ./cmd/shard --shard-id 0 --db-path ./data/shard_0.db`

## How To Run The Working Path
1. Create a `.env` file in the repo root or export environment variables.
2. Required env var: `OPENROUTER_API_KEY`
3. Optional env vars:
   - `OPENROUTER_EMBEDDING_MODEL` default: `text-embedding-3-small`
   - `OPENROUTER_BASE_URL` default: `https://openrouter.ai/api/v1`
4. Start the REPL:
   - `go run ./cmd/coordinator`
5. Use these commands inside the REPL:
   - `insert <text>`
   - `query <k> <text>`
   - `list`
   - `help`
   - `quit`

## Commands:
- `insert` embeds raw text and stores it in an in memory local vector index.
- `query` embeds the query text and runs brute force cosine similarity search over all stored vectors.
- Data does not persist across process restarts.
- This architecture follows the shard approach that will be later implemented.

## SQLite Notes
- The shard/storage path uses `github.com/mattn/go-sqlite3`, so builds that include shard/storage need CGO support.
- `cmd/coordinator` does not use SQLite and can run without CGO.
- If running `cmd/shard` with the default DB path, ensure the `data/` directory exists first.