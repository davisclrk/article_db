package index

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
)

type HNSWConfig struct {
	M              int
	EfConstruction int
	EfSearch       int
	Seed           int64
}

func DefaultHNSWConfig() HNSWConfig {
	return HNSWConfig{
		M:              16,
		EfConstruction: 200,
		EfSearch:       50,
	}
}

type HNSWIndex struct {
	mu        sync.RWMutex
	dimension int

	m        int
	mmax0    int
	efConst  int
	efSearch int
	ml       float64

	maxLevel   int
	entryPoint int

	nodes []hnswNode
	rng   *rand.Rand
}

type hnswNode struct {
	record      Record
	norm        float64
	connections [][]int
	level       int
}

func NewHNSWIndex(cfg HNSWConfig) *HNSWIndex {
	if cfg.M <= 0 {
		cfg.M = 16
	}
	if cfg.EfConstruction <= 0 {
		cfg.EfConstruction = 200
	}
	if cfg.EfSearch <= 0 {
		cfg.EfSearch = 50
	}

	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	return &HNSWIndex{
		m:          cfg.M,
		mmax0:      2 * cfg.M,
		efConst:    cfg.EfConstruction,
		efSearch:   cfg.EfSearch,
		ml:         1.0 / math.Log(float64(cfg.M)),
		maxLevel:   -1,
		entryPoint: -1,
		rng:        rand.New(rand.NewSource(seed)),
	}
}

func (h *HNSWIndex) SetEfSearch(ef int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ef > 0 {
		h.efSearch = ef
	}
}

func (h *HNSWIndex) Insert(id, text string, vector []float32) error {
	if id == "" {
		return fmt.Errorf("id cannot be empty")
	}
	norm, err := vectorNorm(vector)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.dimension == 0 {
		h.dimension = len(vector)
	} else if len(vector) != h.dimension {
		return fmt.Errorf("vector dimension mismatch: got %d want %d", len(vector), h.dimension)
	}

	level := h.randomLevel()
	node := hnswNode{
		record: Record{
			ID:     id,
			Text:   text,
			Vector: append([]float32(nil), vector...),
		},
		norm:        norm,
		connections: make([][]int, level+1),
		level:       level,
	}
	nodeIdx := len(h.nodes)
	h.nodes = append(h.nodes, node)

	if h.entryPoint == -1 {
		h.entryPoint = nodeIdx
		h.maxLevel = level
		return nil
	}

	ep := h.entryPoint

	for lc := h.maxLevel; lc > level; lc-- {
		ep = h.greedyClosest(vector, norm, ep, lc)
	}

	topLevel := level
	if topLevel > h.maxLevel {
		topLevel = h.maxLevel
	}

	for lc := topLevel; lc >= 0; lc-- {
		candidates := h.searchLayer(vector, norm, ep, h.efConst, lc)

		mMax := h.m
		if lc == 0 {
			mMax = h.mmax0
		}
		selected := h.selectNeighborsSimple(candidates, mMax)

		h.nodes[nodeIdx].connections[lc] = make([]int, len(selected))
		for i, s := range selected {
			h.nodes[nodeIdx].connections[lc][i] = s.id
		}

		for _, s := range selected {
			nConns := append(h.nodes[s.id].connections[lc], nodeIdx)
			nMMax := h.m
			if lc == 0 {
				nMMax = h.mmax0
			}
			if len(nConns) > nMMax {
				nConns = h.pruneConnections(s.id, nConns, nMMax)
			}
			h.nodes[s.id].connections[lc] = nConns
		}

		if len(candidates) > 0 {
			ep = candidates[0].id
		}
	}

	if level > h.maxLevel {
		h.maxLevel = level
		h.entryPoint = nodeIdx
	}

	return nil
}

func (h *HNSWIndex) Search(query []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		return []SearchResult{}, nil
	}

	queryNorm, err := vectorNorm(query)
	if err != nil {
		return nil, err
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.nodes) == 0 {
		return []SearchResult{}, nil
	}
	if len(query) != h.dimension {
		return nil, fmt.Errorf("query dimension mismatch: got %d want %d", len(query), h.dimension)
	}

	ep := h.entryPoint
	for lc := h.maxLevel; lc > 0; lc-- {
		ep = h.greedyClosest(query, queryNorm, ep, lc)
	}

	ef := h.efSearch
	if ef < k {
		ef = k
	}
	candidates := h.searchLayer(query, queryNorm, ep, ef, 0)

	if len(candidates) > k {
		candidates = candidates[:k]
	}

	results := make([]SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = SearchResult{
			ID:    h.nodes[c.id].record.ID,
			Text:  h.nodes[c.id].record.Text,
			Score: c.similarity,
		}
	}
	return results, nil
}

func (h *HNSWIndex) List() []Record {
	h.mu.RLock()
	defer h.mu.RUnlock()

	records := make([]Record, len(h.nodes))
	for i, node := range h.nodes {
		records[i] = Record{
			ID:     node.record.ID,
			Text:   node.record.Text,
			Vector: append([]float32(nil), node.record.Vector...),
		}
	}
	return records
}

func (h *HNSWIndex) randomLevel() int {
	r := h.rng.Float64()
	if r == 0 {
		r = 1e-10
	}
	return int(math.Floor(-math.Log(r) * h.ml))
}

func (h *HNSWIndex) greedyClosest(query []float32, queryNorm float64, ep int, layer int) int {
	current := ep
	bestSim := h.nodeSimilarity(query, queryNorm, current)

	for {
		improved := false
		for _, neighbor := range h.nodeConnections(current, layer) {
			sim := h.nodeSimilarity(query, queryNorm, neighbor)
			if sim > bestSim {
				bestSim = sim
				current = neighbor
				improved = true
			}
		}
		if !improved {
			return current
		}
	}
}

func (h *HNSWIndex) searchLayer(query []float32, queryNorm float64, ep int, ef int, layer int) []candidate {
	visited := make(map[int]bool)
	visited[ep] = true

	epSim := h.nodeSimilarity(query, queryNorm, ep)
	initial := candidate{id: ep, similarity: epSim}

	cands := &maxSimHeap{initial}
	heap.Init(cands)

	results := &minSimHeap{initial}
	heap.Init(results)

	for cands.Len() > 0 {
		c := heap.Pop(cands).(candidate)

		if c.similarity < (*results)[0].similarity {
			break
		}

		for _, neighborID := range h.nodeConnections(c.id, layer) {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			sim := h.nodeSimilarity(query, queryNorm, neighborID)
			if sim > (*results)[0].similarity || results.Len() < ef {
				heap.Push(cands, candidate{id: neighborID, similarity: sim})
				heap.Push(results, candidate{id: neighborID, similarity: sim})
				if results.Len() > ef {
					heap.Pop(results)
				}
			}
		}
	}

	out := make([]candidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		out[i] = heap.Pop(results).(candidate)
	}
	return out
}

func (h *HNSWIndex) selectNeighborsSimple(candidates []candidate, m int) []candidate {
	if len(candidates) <= m {
		return candidates
	}
	return candidates[:m]
}

func (h *HNSWIndex) pruneConnections(nodeID int, connections []int, maxConns int) []int {
	node := &h.nodes[nodeID]
	type scored struct {
		id  int
		sim float64
	}
	items := make([]scored, len(connections))
	for i, connID := range connections {
		items[i] = scored{
			id:  connID,
			sim: cosineSimilarity(node.record.Vector, node.norm, h.nodes[connID].record.Vector, h.nodes[connID].norm),
		}
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].sim > items[b].sim
	})
	result := make([]int, maxConns)
	for i := 0; i < maxConns; i++ {
		result[i] = items[i].id
	}
	return result
}

func (h *HNSWIndex) nodeConnections(nodeID int, layer int) []int {
	if layer >= len(h.nodes[nodeID].connections) {
		return nil
	}
	return h.nodes[nodeID].connections[layer]
}

func (h *HNSWIndex) nodeSimilarity(query []float32, queryNorm float64, nodeID int) float64 {
	return cosineSimilarity(query, queryNorm, h.nodes[nodeID].record.Vector, h.nodes[nodeID].norm)
}

type candidate struct {
	id         int
	similarity float64
}

type maxSimHeap []candidate

func (h maxSimHeap) Len() int           { return len(h) }
func (h maxSimHeap) Less(i, j int) bool { return h[i].similarity > h[j].similarity }
func (h maxSimHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxSimHeap) Push(x any)        { *h = append(*h, x.(candidate)) }
func (h *maxSimHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type minSimHeap []candidate

func (h minSimHeap) Len() int           { return len(h) }
func (h minSimHeap) Less(i, j int) bool { return h[i].similarity < h[j].similarity }
func (h minSimHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minSimHeap) Push(x any)        { *h = append(*h, x.(candidate)) }
func (h *minSimHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
