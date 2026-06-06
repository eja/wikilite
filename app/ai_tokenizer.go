// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"fmt"
	"sort"
	"strings"
)

type bpeTokenizer struct {
	vocab      map[string]int
	mergeRank  map[[2]string]int
	byteToChar [256]rune
	eosID      int
}

func loadTokenizer(p *GGUFParser) (*bpeTokenizer, error) {
	tokensRaw, ok := p.Metadata["tokenizer.ggml.tokens"]
	if !ok {
		return nil, fmt.Errorf("tokenizer.ggml.tokens not found in GGUF metadata")
	}
	tokensArr, ok := tokensRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid tokens metadata structure")
	}

	vocab := make(map[string]int)
	for i, tokVal := range tokensArr {
		if s, ok := tokVal.(string); ok {
			vocab[s] = i
		}
	}

	mergeRank := make(map[[2]string]int)
	if mergesRaw, ok := p.Metadata["tokenizer.ggml.merges"]; ok {
		if mergesArr, ok := mergesRaw.([]any); ok {
			for i, mVal := range mergesArr {
				if m, ok := mVal.(string); ok {
					parts := strings.SplitN(m, " ", 2)
					if len(parts) == 2 {
						mergeRank[[2]string{parts[0], parts[1]}] = i
					}
				}
			}
		}
	}

	eosID := 151643
	if eosVal, ok := p.Metadata["tokenizer.ggml.eos_token_id"]; ok {
		if id, ok := eosVal.(uint32); ok {
			eosID = int(id)
		}
	}

	t := &bpeTokenizer{
		vocab:     vocab,
		mergeRank: mergeRank,
		eosID:     eosID,
	}
	t.buildByteMap()

	return t, nil
}

func (t *bpeTokenizer) encode(text string) []int {
	segments := t.splitOnSpecials(text)
	var ids []int
	for _, seg := range segments {
		if t.isSpecialToken(seg) {
			if id, ok := t.vocab[seg]; ok {
				ids = append(ids, id)
			}
		} else {
			ids = append(ids, t.bpeSegment(seg)...)
		}
	}
	ids = append(ids, t.eosID)
	return ids
}

func (t *bpeTokenizer) isSpecialToken(s string) bool {
	return len(s) > 4 && s[0] == '<' && s[1] == '|' && s[len(s)-2] == '|' && s[len(s)-1] == '>'
}

func (t *bpeTokenizer) splitOnSpecials(text string) []string {
	var specials []string
	for tok := range t.vocab {
		if t.isSpecialToken(tok) {
			specials = append(specials, tok)
		}
	}
	sort.Slice(specials, func(i, j int) bool { return len(specials[i]) > len(specials[j]) })

	result := []string{text}
	for _, sp := range specials {
		var next []string
		for _, seg := range result {
			if t.isSpecialToken(seg) {
				next = append(next, seg)
				continue
			}
			parts := strings.Split(seg, sp)
			for i, p := range parts {
				if i > 0 {
					next = append(next, sp)
				}
				if p != "" {
					next = append(next, p)
				}
			}
		}
		result = next
	}
	return result
}

func (t *bpeTokenizer) bpeSegment(text string) []int {
	if text == "" {
		return nil
	}
	rawBytes := []byte(text)
	symbols := make([]string, len(rawBytes))
	for i, b := range rawBytes {
		symbols[i] = string(t.byteToChar[b])
	}
	for {
		bestRank, bestIdx := -1, -1
		for i := 0; i < len(symbols)-1; i++ {
			if rank, ok := t.mergeRank[[2]string{symbols[i], symbols[i+1]}]; ok {
				if bestRank == -1 || rank < bestRank {
					bestRank, bestIdx = rank, i
				}
			}
		}
		if bestIdx == -1 {
			break
		}
		merged := symbols[bestIdx] + symbols[bestIdx+1]
		symbols = append(symbols[:bestIdx], append([]string{merged}, symbols[bestIdx+2:]...)...)
	}
	ids := make([]int, 0, len(symbols))
	for _, s := range symbols {
		if id, ok := t.vocab[s]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func (t *bpeTokenizer) buildByteMap() {
	inBase := func(b int) bool {
		return (b >= '!' && b <= '~') || (b >= 0xA1 && b <= 0xAC) || (b >= 0xAE && b <= 0xFF)
	}
	next := 256
	for b := range 256 {
		if inBase(b) {
			t.byteToChar[b] = rune(b)
		} else {
			t.byteToChar[b] = rune(next)
			next++
		}
	}
}
