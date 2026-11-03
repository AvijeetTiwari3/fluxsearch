// Package trie implements a compressed Patricia trie (radix tree) for
// O(K) prefix-based autocomplete queries, where K = prefix length.
//
// This is a pure in-memory implementation optimised for read-heavy workloads
// (many concurrent prefix queries, infrequent inserts during re-indexing).
//
// Thread-safety: RWMutex protects the root; reads are fully concurrent.
package trie

import (
	"sort"
	"strings"
	"sync"
)

// TrieNode represents a node in the compressed Patricia trie.
type TrieNode struct {
	children map[rune]*TrieNode
	edge     string // compressed edge label (Patricia compression)
	isEnd    bool
	count    uint32 // search frequency — used to rank suggestions
}

// PrefixTrie is the top-level autocomplete structure.
type PrefixTrie struct {
	mu   sync.RWMutex
	root *TrieNode
	size int // total number of unique terms
}

// NewPrefixTrie creates an empty trie.
func NewPrefixTrie() *PrefixTrie {
	return &PrefixTrie{
		root: &TrieNode{children: make(map[rune]*TrieNode)},
	}
}

// Insert adds a term to the trie with an initial frequency count.
func (t *PrefixTrie) Insert(term string, count uint32) {
	if term == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.insert(t.root, []rune(term), count)
}

func (t *PrefixTrie) insert(node *TrieNode, runes []rune, count uint32) {
	if len(runes) == 0 {
		if !node.isEnd {
			node.isEnd = true
			t.size++
		}
		node.count += count
		return
	}

	first := runes[0]
	child, ok := node.children[first]
	if !ok {
		// Create new leaf node
		newNode := &TrieNode{
			children: make(map[rune]*TrieNode),
			edge:     string(runes),
			isEnd:    true,
			count:    count,
		}
		node.children[first] = newNode
		t.size++
		return
	}

	// Find common prefix between edge and remaining runes
	edgeRunes := []rune(child.edge)
	common := commonPrefixLen(edgeRunes, runes)

	if common == len(edgeRunes) {
		// Entire edge matches — recurse into child
		t.insert(child, runes[common:], count)
		return
	}

	// Split edge at common prefix (Patricia compression)
	splitNode := &TrieNode{
		children: make(map[rune]*TrieNode),
		edge:     string(edgeRunes[:common]),
	}

	// Old child keeps the rest of its edge
	child.edge = string(edgeRunes[common:])
	splitNode.children[edgeRunes[common]] = child

	// New branch for the diverging suffix
	if common < len(runes) {
		newNode := &TrieNode{
			children: make(map[rune]*TrieNode),
			edge:     string(runes[common:]),
			isEnd:    true,
			count:    count,
		}
		splitNode.children[runes[common]] = newNode
		t.size++
	} else {
		splitNode.isEnd = true
		splitNode.count = count
	}

	node.children[first] = splitNode
}

func commonPrefixLen(a, b []rune) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Suggestion holds an autocomplete candidate with its search frequency.
type Suggestion struct {
	Term  string
	Count uint32
}

// Complete returns up to maxResults autocomplete suggestions for prefix.
// Results are sorted by descending frequency (most searched first).
func (t *PrefixTrie) Complete(prefix string, maxResults int) []Suggestion {
	if maxResults <= 0 {
		maxResults = 10
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	prefixRunes := []rune(strings.ToLower(prefix))
	node, matchedPrefix := t.findPrefixNode(t.root, prefixRunes, "")
	if node == nil {
		return nil
	}

	var results []Suggestion
	t.collect(node, matchedPrefix, &results)

	// Sort by frequency descending, then lexicographic
	sort.Slice(results, func(i, j int) bool {
		if results[i].Count != results[j].Count {
			return results[i].Count > results[j].Count
		}
		return results[i].Term < results[j].Term
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

// findPrefixNode traverses the trie to find the node where prefix ends.
// Returns the node and the string built so far (for suffix collection).
func (t *PrefixTrie) findPrefixNode(node *TrieNode, prefix []rune, built string) (*TrieNode, string) {
	if len(prefix) == 0 {
		return node, built
	}

	first := prefix[0]
	child, ok := node.children[first]
	if !ok {
		return nil, ""
	}

	edgeRunes := []rune(child.edge)
	common := commonPrefixLen(edgeRunes, prefix)

	if common < len(prefix) && common == len(edgeRunes) {
		// Consumed full edge — keep traversing
		return t.findPrefixNode(child, prefix[common:], built+child.edge)
	}

	if common == len(prefix) {
		// Prefix ends within this edge — valid prefix found
		return child, built + child.edge
	}

	return nil, "" // prefix not in trie
}

// collect recursively gathers all complete terms rooted at node.
func (t *PrefixTrie) collect(node *TrieNode, currentTerm string, results *[]Suggestion) {
	if node.isEnd {
		*results = append(*results, Suggestion{Term: currentTerm, Count: node.count})
	}
	for _, child := range node.children {
		t.collect(child, currentTerm+child.edge, results)
	}
}

// IncrementCount increases the search frequency for a term by 1.
// Call this on every successful query hit to keep suggestions fresh.
func (t *PrefixTrie) IncrementCount(term string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefixRunes := []rune(term)
	node, _ := t.findPrefixNode(t.root, prefixRunes, "")
	if node != nil && node.isEnd {
		node.count++
	}
}

// Size returns the number of unique terms in the trie.
func (t *PrefixTrie) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}
