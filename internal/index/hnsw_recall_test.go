package index

import (
	"math"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHNSWRecallVsBruteForce measures HNSW recall@10 and per-query latency against
// the exact brute-force index on a synthetic corpus shaped like text embeddings.
//
// With no environment variables set it runs a small corpus in a few seconds and
// acts as a regression test on recall at the production efSearch. For a full-size
// measurement run:
//
//	HNSW_BENCH_N=100000 go test ./internal/index -run TestHNSWRecallVsBruteForce -v -timeout 3h
//
// Knobs:
//
//	HNSW_BENCH_N        corpus size (default 2000)
//	HNSW_BENCH_QUERIES  number of held-out queries (default 1000)
//	HNSW_BENCH_DIM      vector dimension (default 1536, matches text-embedding-3-small)
//	HNSW_BENCH_EF       comma-separated efSearch values to sweep (default "50,100,200")
//	HNSW_BENCH_SEED     RNG seed for both the data and the graph (default 42)
//	HNSW_BENCH_DATA     "clustered" (default, embedding-like) or "uniform" (control)
//	HNSW_BENCH_NOISE    per-point noise variance for clustered data, as a fraction
//	                    of the cluster-center norm (default 0.3; larger overlaps
//	                    clusters and makes neighbors more ambiguous)
//
// The recall floor is only asserted in the default data configuration
// (clustered, default noise); other configurations are measurement runs.
func TestHNSWRecallVsBruteForce(t *testing.T) {
	const k = 10
	const defaultNoise = 0.3
	// Floor for the regression check at the production efSearch on the default
	// corpus. Recall measured 1.00 for seeds 1-5 and 42 at N=2000, so the floor
	// leaves a wide margin for seed noise while a fragmented graph, which scores
	// 0.4-0.8 on this data, still fails it.
	const minRecallAtDefaultEf = 0.90

	n := envInt(t, "HNSW_BENCH_N", 2000)
	numQueries := envInt(t, "HNSW_BENCH_QUERIES", 1000)
	dim := envInt(t, "HNSW_BENCH_DIM", 1536)
	seed := envInt(t, "HNSW_BENCH_SEED", 42)
	efs := envInts(t, "HNSW_BENCH_EF", []int{50, 100, 200})
	dataKind := os.Getenv("HNSW_BENCH_DATA")
	if dataKind == "" {
		dataKind = "clustered"
	}
	noise := envFloat(t, "HNSW_BENCH_NOISE", defaultNoise)

	rng := rand.New(rand.NewSource(int64(seed)))
	var sample func() []float32
	switch dataKind {
	case "clustered":
		sample = newSynthEmbeddings(rng, n, dim, noise).sample
	case "uniform":
		sample = func() []float32 { return toFloat32(randomUnit(rng, dim)) }
	default:
		t.Fatalf("HNSW_BENCH_DATA=%q: want clustered or uniform", dataKind)
	}

	bf := NewBruteForceIndex()
	cfg := DefaultHNSWConfig()
	cfg.Seed = int64(seed)
	hnsw := NewHNSWIndex(cfg)

	// Insert one vector at a time so the full corpus is never held in memory
	// outside the two indexes (each index keeps its own copy).
	var hnswBuild time.Duration
	for i := 0; i < n; i++ {
		id := strconv.Itoa(i)
		v := sample()
		if err := bf.Insert(id, "", v); err != nil {
			t.Fatalf("brute-force insert %d: %v", i, err)
		}
		start := time.Now()
		if err := hnsw.Insert(id, "", v); err != nil {
			t.Fatalf("hnsw insert %d: %v", i, err)
		}
		hnswBuild += time.Since(start)
	}

	queries := make([][]float32, numQueries)
	for i := range queries {
		queries[i] = sample()
	}

	// Ground truth and brute-force latency.
	truth := make([]map[string]struct{}, numQueries)
	bfLat := make([]time.Duration, numQueries)
	runtime.GC()
	for qi, q := range queries {
		start := time.Now()
		res, err := bf.Search(q, k)
		bfLat[qi] = time.Since(start)
		if err != nil {
			t.Fatalf("brute-force search %d: %v", qi, err)
		}
		if len(res) != k {
			t.Fatalf("brute-force search %d returned %d results, want %d", qi, len(res), k)
		}
		truth[qi] = make(map[string]struct{}, k)
		for _, r := range res {
			truth[qi][r.ID] = struct{}{}
		}
	}
	bfStats := summarize(bfLat)

	t.Logf("corpus=%d dim=%d data=%s noise=%.2f queries=%d k=%d hnsw(M=%d efConstruction=%d) build=%s layer0-reachable-from-entry=%.4f %s/%s cpus=%d %s",
		n, dim, dataKind, noise, numQueries, k, cfg.M, cfg.EfConstruction, hnswBuild.Round(time.Millisecond),
		hnsw.layer0ReachableFromEntry(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	t.Logf("brute-force   mean=%-10s p50=%-10s p99=%s", bfStats.mean, bfStats.p50, bfStats.p99)

	recallAt := map[int]float64{}
	for _, ef := range efs {
		hnsw.SetEfSearch(ef)
		lat := make([]time.Duration, numQueries)
		perQuery := make([]float64, numQueries)
		runtime.GC()
		for qi, q := range queries {
			start := time.Now()
			res, err := hnsw.Search(q, k)
			lat[qi] = time.Since(start)
			if err != nil {
				t.Fatalf("hnsw search %d (ef=%d): %v", qi, ef, err)
			}
			if len(res) != k {
				t.Fatalf("hnsw search %d (ef=%d) returned %d results, want %d", qi, ef, len(res), k)
			}
			hits := 0
			for _, r := range res {
				if _, ok := truth[qi][r.ID]; ok {
					hits++
				}
			}
			perQuery[qi] = float64(hits) / float64(k)
		}
		recall, ci95 := meanAndCI95(perQuery)
		recallAt[ef] = recall
		stats := summarize(lat)
		t.Logf("hnsw ef=%-4d recall@%d=%.4f ±%.4f mean=%-10s p50=%-10s p99=%-10s speedup(mean)=%.1fx speedup(p50)=%.1fx",
			ef, k, recall, ci95, stats.mean, stats.p50, stats.p99,
			float64(bfStats.mean)/float64(stats.mean), float64(bfStats.p50)/float64(stats.p50))
	}

	if dataKind != "clustered" || noise != defaultNoise {
		return
	}
	defaultEf := DefaultHNSWConfig().EfSearch
	if r, ok := recallAt[defaultEf]; ok && r < minRecallAtDefaultEf {
		t.Fatalf("recall@%d at default efSearch=%d is %.4f, below floor %.2f", k, defaultEf, r, minRecallAtDefaultEf)
	}
}

// layer0ReachableFromEntry is the share of nodes reachable from the global entry
// point by walking directed layer-0 links. It is a graph-health indicator rather
// than a strict bound on recall: Search enters layer 0 at whatever node the
// upper-layer descent lands on, not at the global entry point. A well-formed
// graph reads 1.0. A value far below it means layer 0 has split into islands,
// and queries whose true neighbors sit in a different island than the landing
// node cannot reach them no matter how large efSearch is.
func (h *HNSWIndex) layer0ReachableFromEntry() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.nodes) == 0 {
		return 1
	}
	seen := make([]bool, len(h.nodes))
	queue := []int{h.entryPoint}
	seen[h.entryPoint] = true
	reached := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		reached++
		for _, nb := range h.nodeConnections(cur, 0) {
			if !seen[nb] {
				seen[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return float64(reached) / float64(len(h.nodes))
}

// synthEmbeddings samples unit vectors from a two-level Gaussian mixture that
// imitates the geometry of text-embedding spaces: every vector shares a common
// direction (embedding anisotropy), vectors group into topics and subtopics, and
// each point carries its own noise. Uniform random vectors would be useless here
// because in 1536 dimensions they are all nearly equidistant, so any recall
// figure measured on them says nothing about real data.
//
// With the weights below and the default noise variance of 0.3, expected cosine
// similarity is roughly 0.72 within a subtopic, 0.57 across subtopics of one
// topic, and 0.23 across topics, which is in the range seen for related,
// loosely related, and unrelated news articles. Raising the noise variance
// blurs the cluster boundaries and makes the true neighbors harder to separate
// from near-misses.
type synthEmbeddings struct {
	rng        *rand.Rand
	dim        int
	centers    [][]float32
	noiseSigma float64
}

func newSynthEmbeddings(rng *rand.Rand, n, dim int, noiseVariance float64) *synthEmbeddings {
	const subtopicsPerTopic = 16
	const wShared, wTopic, wSub = 0.5, 0.6, 0.4

	topics := n / 1024
	if topics < 4 {
		topics = 4
	}
	if topics > 128 {
		topics = 128
	}

	shared := randomUnit(rng, dim)
	centers := make([][]float32, 0, topics*subtopicsPerTopic)
	for t := 0; t < topics; t++ {
		topic := randomUnit(rng, dim)
		for s := 0; s < subtopicsPerTopic; s++ {
			sub := randomUnit(rng, dim)
			c := make([]float32, dim)
			for d := range c {
				c[d] = float32(wShared*shared[d] + wTopic*topic[d] + wSub*sub[d])
			}
			centers = append(centers, c)
		}
	}

	return &synthEmbeddings{
		rng:        rng,
		dim:        dim,
		centers:    centers,
		noiseSigma: math.Sqrt(noiseVariance / float64(dim)),
	}
}

func (g *synthEmbeddings) sample() []float32 {
	center := g.centers[g.rng.Intn(len(g.centers))]
	v := make([]float32, g.dim)
	for d := range v {
		v[d] = center[d] + float32(g.rng.NormFloat64()*g.noiseSigma)
	}
	normalize(v)
	return v
}

func randomUnit(rng *rand.Rand, dim int) []float64 {
	v := make([]float64, dim)
	var sum float64
	for d := range v {
		v[d] = rng.NormFloat64()
		sum += v[d] * v[d]
	}
	inv := 1 / math.Sqrt(sum)
	for d := range v {
		v[d] *= inv
	}
	return v
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	inv := float32(1 / math.Sqrt(sum))
	for d := range v {
		v[d] *= inv
	}
}

// meanAndCI95 returns the mean of xs and the half-width of its 95% confidence
// interval under a normal approximation (1.96 standard errors).
func meanAndCI95(xs []float64) (mean, halfWidth float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))
	if len(xs) < 2 {
		return mean, 0
	}
	var sq float64
	for _, x := range xs {
		sq += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(sq / float64(len(xs)-1))
	return mean, 1.96 * sd / math.Sqrt(float64(len(xs)))
}

type latencyStats struct {
	mean, p50, p99 time.Duration
}

func summarize(ds []time.Duration) latencyStats {
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	pct := func(p float64) time.Duration {
		idx := int(math.Ceil(p*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	return latencyStats{
		mean: total / time.Duration(len(sorted)),
		p50:  pct(0.50),
		p99:  pct(0.99),
	}
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not an integer: %v", name, raw, err)
	}
	return v
}

func envFloat(t *testing.T, name string, def float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("%s=%q is not a number: %v", name, raw, err)
	}
	return v
}

func envInts(t *testing.T, name string, def []int) []int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	var out []int
	for _, part := range strings.Split(raw, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			t.Fatalf("%s=%q contains a non-integer: %v", name, raw, err)
		}
		out = append(out, v)
	}
	return out
}
