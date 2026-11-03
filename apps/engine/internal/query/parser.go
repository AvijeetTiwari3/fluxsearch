// Package query implements a recursive-descent query parser for FluxSearch.
//
// Supported query syntax:
//
//	term          →  standard BM25 match
//	+term         →  MUST match (AND semantics)
//	-term         →  MUST NOT match (NOT semantics)
//	"phrase here" →  exact phrase match (positional)
//	term*         →  prefix wildcard match
//	term1 term2   →  OR (default)
//
// Grammar (EBNF):
//
//	Query     = Clause { Clause } EOF
//	Clause    = ["+"|"-"] ( Phrase | Prefix | Term )
//	Phrase    = '"' { WORD } '"'
//	Prefix    = WORD '*'
//	Term      = WORD
package query

import (
	"strings"
	"unicode"
)

// ClauseType classifies the Boolean role of a query clause.
type ClauseType uint8

const (
	ClauseSHOULD ClauseType = iota // default OR — document may match
	ClauseMUST                     // +term — document must match
	ClauseMUST_NOT                 // -term — document must not match
)

// Clause represents a single parsed query clause.
type Clause struct {
	Type     ClauseType
	IsPhrase bool     // true for "quoted phrase"
	IsPrefix bool     // true for wildcard prefix*
	Terms    []string // analyzed terms (1 for term/prefix, N for phrase)
	Raw      string   // original raw text before analysis
}

// ParsedQuery holds all clauses extracted from an input query string.
type ParsedQuery struct {
	Clauses    []Clause
	RawInput   string
	HasMust    bool // true if any MUST clause exists
	HasMustNot bool // true if any MUST_NOT clause exists
}

// IsEmpty returns true if the query has no clauses.
func (pq *ParsedQuery) IsEmpty() bool {
	return len(pq.Clauses) == 0
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser
// ─────────────────────────────────────────────────────────────────────────────

// Parser holds the lexer state for recursive descent parsing.
type Parser struct {
	input []rune
	pos   int
	// analyzer is a function applied to each raw term
	analyzer func(string) []string
}

// NewParser creates a parser with a custom analysis function.
// Pass analysis.AnalyzeQuery as the analyzer for standard NLP processing.
func NewParser(analyzer func(string) []string) *Parser {
	return &Parser{analyzer: analyzer}
}

// Parse converts a raw query string into a ParsedQuery.
func (p *Parser) Parse(raw string) *ParsedQuery {
	p.input = []rune(raw)
	p.pos = 0

	pq := &ParsedQuery{RawInput: raw}

	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			break
		}

		clauseType := ClauseSHOULD
		switch p.peek() {
		case '+':
			clauseType = ClauseMUST
			p.advance()
			pq.HasMust = true
		case '-':
			clauseType = ClauseMUST_NOT
			p.advance()
			pq.HasMustNot = true
		}

		p.skipWhitespace()
		if p.pos >= len(p.input) {
			break
		}

		var clause Clause
		clause.Type = clauseType

		if p.peek() == '"' {
			clause = p.parsePhrase(clauseType)
		} else {
			clause = p.parseTerm(clauseType)
		}

		if len(clause.Terms) > 0 {
			pq.Clauses = append(pq.Clauses, clause)
		}
	}

	return pq
}

// parsePhrase reads a "quoted phrase" and returns its analyzed tokens.
func (p *Parser) parsePhrase(ct ClauseType) Clause {
	p.advance() // consume opening '"'
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != '"' {
		p.pos++
	}
	raw := string(p.input[start:p.pos])
	if p.pos < len(p.input) {
		p.advance() // consume closing '"'
	}

	terms := p.analyzer(raw)
	return Clause{
		Type:     ct,
		IsPhrase: true,
		Terms:    terms,
		Raw:      raw,
	}
}

// parseTerm reads a single term or prefix wildcard.
func (p *Parser) parseTerm(ct ClauseType) Clause {
	start := p.pos
	for p.pos < len(p.input) && !unicode.IsSpace(p.input[p.pos]) && p.input[p.pos] != '"' {
		p.pos++
	}
	raw := string(p.input[start:p.pos])

	isPrefix := strings.HasSuffix(raw, "*")
	if isPrefix {
		raw = raw[:len(raw)-1]
	}

	terms := p.analyzer(raw)
	return Clause{
		Type:     ct,
		IsPrefix: isPrefix,
		Terms:    terms,
		Raw:      raw,
	}
}

func (p *Parser) peek() rune {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *Parser) advance() {
	if p.pos < len(p.input) {
		p.pos++
	}
}

func (p *Parser) skipWhitespace() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}
