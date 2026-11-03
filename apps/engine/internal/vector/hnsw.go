// Package vector implements a Hierarchical Navigable Small World (HNSW) graph
// for approximate nearest-neighbour (ANN) search over dense float32 vectors.
//
// HNSW Overview (Malkov & Yashunin, 2018):
//   - Multilayer graph: each node exists on layers [0, L] where L ~ Poisson(1/ln(M))
//   - Layer 0 has all nodes; higher layers have exponentially fewer
//   - Greedy beam search navigates from top layer down to layer 0
//   - Guaranteed O(log N) search complexity on clustered datasets
//
// Parameters:
//   - M      = 16   (max connections per node per layer)
//   - Mmax   = 32   (max connections at layer 0)
//   - efC    = 200  (construction beam width)
//   - efS    = 50   (search beam width, tunable at query time)
//
// Distance metric: cosine similarity (converted to distance: 1 - cosine)
package vector

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

const (
	// HNSW hyperparameters
	DefaultM    = 16  // max connections per layer (above 0)
	DefaultMmax = 32  // max connections at layer 0
	DefaultEfC  = 200 // beam width during construction
	DefaultEfS  = 50  // beam width during search
)

// VectorID is a unique identifier for an indexed vector (matches DocID).
type VectorID uint64

// Node represents a single vector in the HNSW graph.
type Node struct {
	ID        VectorID
	Vector    []float32
	Neighbors [][]VectorID // Neighbors[layer] = neighbor IDs at that layer
}

// HNSWIndex is a concurrent HNSW approximate nearest-neighbour graph.
type HNSWIndex struct {
	mu       sync.RWMutex
	nodes    map[VectorID]*Node
	entryID  VectorID // top-layer entry point
	maxLevel int      // current maximum layer in the graph
	dim      int      // vector dimension

	M    int
	Mmax int
	EfC  int

	rng *rand.Rand
}

// NewHNSWIndex creates a new HNSW index for vectors of dimension dim.
func NewHNSWIndex(dim int) *HNSWIndex {
	return &HNSWIndex{
		nodes: make(map[VectorID]*Node),
		dim:   dim,
		M:     DefaultM,
		Mmax:  DefaultMmax,
		EfC:   DefaultEfC,
		rng:   rand.New(rand.NewSource(42)),
	}
}

// randomLevel generates a random layer assignment. Level probability: P(l) = (1/ln(M))^l
func (h *HNSWIndex) randomLevel() int {
	ml := 1.0 / math.Log(float64(h.M))
	level := 0
	for h.rng.Float64() < ml && level < 32 {
		level++
	}
	return level
}

// Insert adds a new vector to the HNSW graph. Thread-safe.
func (h *HNSWIndex) Insert(id VectorID, vector []float32) {
	if len(vector) != h.dim {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	level := h.randomLevel()
	node := &Node{
		ID:        id,
		Vector:    make([]float32, h.dim),
		Neighbors: make([][]VectorID, level+1),
	}
	copy(node.Vector, vector)
	for l := range node.Neighbors {
		node.Neighbors[l] = make([]VectorID, 0, h.maxConns(l))
	}

	h.nodes[id] = node

	if len(h.nodes) == 1 {
		h.entryID = id
		h.maxLevel = level
		return
	}

	entryID := h.entryID
	currMaxLevel := h.maxLevel

	// Phase 1: Greedy descent from top layer to (level+1) — find entry point
	for l := currMaxLevel; l > level; l-- {
		entryID = h.greedyDescend(vector, entryID, l)
	}

	// Phase 2: Beam search + bidirectional link at each layer from min(level, maxLevel) to 0
	for l := min(level, currMaxLevel); l >= 0; l-- {
		maxConns := h.maxConns(l)
		candidates := h.searchLayer(vector, entryID, h.EfC, l)

		// Select M nearest neighbors
		neighbors := selectNeighbors(candidates, maxConns)

		// Set bidirectional connections
		node.Neighbors[l] = make([]VectorID, 0, len(neighbors))
		for _, nb := range neighbors {
			node.Neighbors[l] = append(node.Neighbors[l], nb.id)

			// Add reverse connection, pruning if > Mmax
			nbNode := h.nodes[nb.id]
			if l < len(nbNode.Neighbors) {
				nbNode.Neighbors[l] = append(nbNode.Neighbors[l], id)
				if len(nbNode.Neighbors[l]) > maxConns {
					// Prune: keep closest maxConns
					pruned := h.pruneConnections(nbNode.Vector, nbNode.Neighbors[l], maxConns)
					nbNode.Neighbors[l] = pruned
				}
			}
		}

		if len(candidates) > 0 {
			entryID = candidates[0].id // best candidate becomes new entry
		}
	}

	if level > currMaxLevel {
		h.maxLevel = level
		h.entryID = id
	}
}

// Search returns the ef nearest neighbours to query vector, with distances.
func (h *HNSWIndex) Search(query []float32, ef int) []SearchResult {
	if ef <= 0 {
		ef = DefaultEfS
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.nodes) == 0 {
		return nil
	}

	entryID := h.entryID
	// Greedy descent to layer 1
	for l := h.maxLevel; l > 0; l-- {
		entryID = h.greedyDescend(query, entryID, l)
	}

	// Beam search at layer 0
	candidates := h.searchLayer(query, entryID, ef, 0)

	results := make([]SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = SearchResult{ID: c.id, Distance: c.dist}
	}
	return results
}

// SearchResult holds an ANN search result.
type SearchResult struct {
	ID       VectorID
	Distance float32 // cosine distance: 1 - cosine_similarity
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal HNSW helpers
// ─────────────────────────────────────────────────────────────────────────────

type candidate struct {
	id   VectorID
	dist float32
}

// greedyDescend runs greedy single-step descent on layer l, starting from entry.
func (h *HNSWIndex) greedyDescend(query []float32, entry VectorID, layer int) VectorID {
	best := entry
	bestDist := cosineDistance(query, h.nodes[entry].Vector)

	for {
		improved := false
		node := h.nodes[best]
		if layer >= len(node.Neighbors) {
			break
		}
		for _, nbID := range node.Neighbors[layer] {
			nb, ok := h.nodes[nbID]
			if !ok {
				continue
			}
			d := cosineDistance(query, nb.Vector)
			if d < bestDist {
				bestDist = d
				best = nbID
				improved = true
			}
		}
		if !improved {
			break
		}
	}
	return best
}

// searchLayer runs an ef-wide beam search at layer l from entry.
// Returns candidates sorted by ascending distance.
func (h *HNSWIndex) searchLayer(query []float32, entry VectorID, ef int, layer int) []candidate {
	visited := make(map[VectorID]struct{})
	visited[entry] = struct{}{}

	entryDist := cosineDistance(query, h.nodes[entry].Vector)
	candidates := []candidate{{id: entry, dist: entryDist}}
	results := []candidate{{id: entry, dist: entryDist}}

	for len(candidates) > 0 {
		// Pop closest candidate
		c := candidates[0]
		candidates = candidates[1:]

		// If worst result is closer than current candidate, we're done
		if len(results) >= ef && c.dist > results[len(results)-1].dist {
			break
		}

		node := h.nodes[c.id]
		if layer >= len(node.Neighbors) {
			continue
		}

		for _, nbID := range node.Neighbors[layer] {
			if _, seen := visited[nbID]; seen {
				continue
			}
			visited[nbID] = struct{}{}

			nb, ok := h.nodes[nbID]
			if !ok {
				continue
			}
			d := cosineDistance(query, nb.Vector)

			if len(results) < ef || d < results[len(results)-1].dist {
				candidates = append(candidates, candidate{id: nbID, dist: d})
				results = append(results, candidate{id: nbID, dist: d})
				sort.Slice(results, func(i, j int) bool { return results[i].dist < results[j].dist })
				if len(results) > ef {
					results = results[:ef]
				}
				sort.Slice(candidates, func(i, j int) bool { return candidates[i].dist < candidates[j].dist })
			}
		}
	}
	return results
}

// selectNeighbors picks the M closest candidates (simple greedy selection).
func selectNeighbors(candidates []candidate, m int) []candidate {
	if len(candidates) <= m {
		return candidates
	}
	return candidates[:m]
}

// pruneConnections keeps the maxConns closest neighbours for a node.
func (h *HNSWIndex) pruneConnections(origin []float32, neighbors []VectorID, maxConns int) []VectorID {
	type nb struct {
		id   VectorID
		dist float32
	}
	scored := make([]nb, 0, len(neighbors))
	for _, nid := range neighbors {
		n, ok := h.nodes[nid]
		if !ok {
			continue
		}
		scored = append(scored, nb{id: nid, dist: cosineDistance(origin, n.Vector)})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].dist < scored[j].dist })
	if len(scored) > maxConns {
		scored = scored[:maxConns]
	}
	out := make([]VectorID, len(scored))
	for i, s := range scored {
		out[i] = s.id
	}
	return out
}

func (h *HNSWIndex) maxConns(layer int) int {
	if layer == 0 {
		return h.Mmax
	}
	return h.M
}

// ─────────────────────────────────────────────────────────────────────────────
// Cosine Distance
// ─────────────────────────────────────────────────────────────────────────────

// cosineDistance computes 1 - cosine_similarity(a, b).
// Returns 1.0 if either vector is zero.
func cosineDistance(a, b []float32) float32 {
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 1.0
	}
	cos := dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	if cos > 1.0 {
		cos = 1.0
	}
	return 1.0 - cos
}

// CosineSimilarity returns the cosine similarity in [0, 1].
func CosineSimilarity(a, b []float32) float32 {
	return 1.0 - cosineDistance(a, b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Size returns the number of indexed vectors.
func (h *HNSWIndex) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

// Dim returns the vector dimension this index was created with.
func (h *HNSWIndex) Dim() int {
	return h.dim
}
