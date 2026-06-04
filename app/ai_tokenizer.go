// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"encoding/json"
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

type tokenizerFile struct {
	Model struct {
		Vocab  map[string]int  `json:"vocab"`
		Merges json.RawMessage `json:"merges"`
	} `json:"model"`
	AddedTokens []struct {
		ID      int    `json:"id"`
		Content string `json:"content"`
	} `json:"added_tokens"`
}

func loadTokenizer(fs modelFS) (*bpeTokenizer, error) {
	data, err := fs.ReadFile("tokenizer.json")
	if err != nil {
		return nil, fmt.Errorf("read tokenizer.json: %w", err)
	}
	var tf tokenizerFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parse tokenizer.json: %w", err)
	}

	t := &bpeTokenizer{
		vocab: tf.Model.Vocab,
		eosID: 151643,
	}
	for _, at := range tf.AddedTokens {
		t.vocab[at.Content] = at.ID
	}

	var mergesStr []string
	var mergesArr [][]string
	if len(tf.Model.Merges) > 0 && string(tf.Model.Merges) != "null" {
		if err := json.Unmarshal(tf.Model.Merges, &mergesStr); err == nil {
			t.mergeRank = make(map[[2]string]int, len(mergesStr))
			for i, m := range mergesStr {
				parts := strings.SplitN(m, " ", 2)
				if len(parts) == 2 {
					t.mergeRank[[2]string{parts[0], parts[1]}] = i
				}
			}
		} else if err := json.Unmarshal(tf.Model.Merges, &mergesArr); err == nil {
			t.mergeRank = make(map[[2]string]int, len(mergesArr))
			for i, parts := range mergesArr {
				if len(parts) == 2 {
					t.mergeRank[[2]string{parts[0], parts[1]}] = i
				}
			}
		} else {
			return nil, fmt.Errorf("tokenizer.json merges format not recognized")
		}
	} else {
		t.mergeRank = make(map[[2]string]int)
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
