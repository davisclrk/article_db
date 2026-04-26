# Distributed Semantic Search Vector Database

A distributed semantic search vector database implementation built from scratch in Go and SQLite.

The database ingests articles, extracts main text and generates a short summary, embeds the combined text (headline + short summary) into a vector, and operates using cosine similarity for the semantic search. Articles are partitioned across shard nodes and replicated (R = 2) for redundancy and fault tolerance.

---

## Overview

High-level ingestion flow:
1. User submits a URL of a news article
2. Server extracts article text, headline, and creates a short summary (initially implemented as the first N sentences of the article, currently N = 3)
3. Server constructs `search_text` string and embeds it into a vector utilzing the OpenRouter API (utilizing the text-embedding-3-small model)
4. Vector + metadata are stored on a shard node with an exact copy on a replica node, and the coordinator returns success only after both writes succeed

High-level query flow:
1. User submits a text query of articles they are interested in
2. Query text is embedded and broadcasted to shard primaries
3. Shards return top-k candidate matches
4. The coordinator merges and returns global top-k results, deduplicating the results (if any duplicates exist)

---

## Architecture

The system consists of:

- **Coordinator (Main) Node**
  - Receives requests from users
  - Runs ingestion and query pipeline
  - Handles vector embedding operations and article summarization
  - Routes writes/reads to shards
  - Merges results from shards for queries

- **Shard Nodes**
  - Store a partition of the embedded data
  - Maintain a local vector index (brute-force first; HNSW later)
  - Execute nearest neighbor search queries for the embedded data
  - Replicate shard data for redundancy

Documents will be assigned to logical shards based on the hash of the document ID modulo the number of shards, i.e. `shard_id = hash(document_id) % num_shards`. Every logical shard will have a primary and a replica node. The coordinator stores the shard routing map and writes to primaries. Each primary writes to its replica before acknowledging success. Reads are served from primaries by default. If a primary becomes unavailable, reads for that shard can fail over to the replica, but writes fail until both nodes are available again. Each shard uses SQLite as its local storage engine. The client only communicates with the coordinator.

---

## Operations
- **Insert(link)** — Given an article link, store it as a new entry in the database. Returns the ID of the entry.
- **Delete(id)** — Given an ID, delete the corresponding entry from the database.
- **Query(text, k)** — Given text query, return top-k similar results.
- **Get(id)** — Return article, headline, and summary from ID.