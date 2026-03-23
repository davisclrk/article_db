package models

type ShardInfo struct {
	ShardID     int    `json:"shard_id"`
	PrimaryAddr string `json:"primary_addr"`
	ReplicaAddr string `json:"replica_addr"`
	IsHealthy   bool   `json:"is_healthy"`
}

type ShardMap struct {
	NumShards int                `json:"num_shards"`
	Shards    map[int]*ShardInfo `json:"shards"`
}

func NewShardMap(numShards int) *ShardMap {
	shards := make(map[int]*ShardInfo)
	for i := 0; i < numShards; i++ {
		shards[i] = &ShardInfo{
			ShardID:   i,
			IsHealthy: true,
		}
	}
	return &ShardMap{
		NumShards: numShards,
		Shards:    shards,
	}
}

func (sm *ShardMap) GetShardForID(docID string) int {
	if sm.NumShards <= 0 {
		return 0
	}
	hash := hashString(docID)
	shard := hash % sm.NumShards
	if shard < 0 {
		shard += sm.NumShards
	}
	return shard
}

func hashString(s string) int {
	h := 0
	for _, c := range s {
		h = 31*h + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}
