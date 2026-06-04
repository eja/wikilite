// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	cfgVocabSize        = 151669
	cfgHiddenSize       = 1024
	cfgNumLayers        = 28
	cfgNumHeads         = 16
	cfgNumKVHeads       = 8
	cfgHeadDim          = 128
	cfgIntermediateSize = 3072
	cfgRopeTheta        = 1000000.0
	cfgRMSNormEps       = 1e-6
)

type tensorInfo struct {
	Dtype       string `json:"dtype"`
	Shape       []int  `json:"shape"`
	DataOffsets [2]int `json:"data_offsets"`
}

type safeTensors struct {
	data map[string][]float32
}

type qwen3Model struct {
	embedTokens []float32
	normWeight  []float32
	layers      []qwen3Layer
}

type qwen3Layer struct {
	inputNorm []float32
	qProj     []float32
	qBias     []float32
	kProj     []float32
	kBias     []float32
	vProj     []float32
	vBias     []float32
	oProj     []float32
	qNorm     []float32
	kNorm     []float32
	postNorm  []float32
	gateProj  []float32
	upProj    []float32
	downProj  []float32
}

func (st *safeTensors) get(name string) ([]float32, error) {
	v, ok := st.data[name]
	if !ok {
		return nil, fmt.Errorf("tensor %q not found in weights", name)
	}
	return v, nil
}

func loadSafetensorsDir(fs modelFS) (*safeTensors, error) {
	files, err := fs.ListFiles()
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	st := &safeTensors{data: make(map[string][]float32)}
	loaded := 0
	for _, filename := range files {
		if filepath.Ext(filename) != ".safetensors" {
			continue
		}
		if err := loadOneSafetensors(fs, filename, st); err != nil {
			return nil, fmt.Errorf("%s: %w", filename, err)
		}
		loaded++
	}
	if loaded == 0 {
		return nil, fmt.Errorf("no .safetensors files found")
	}
	return st, nil
}

func loadOneSafetensors(fs modelFS, filename string, st *safeTensors) error {
	data, err := fs.ReadFile(filename)
	if err != nil {
		return err
	}
	f := bytes.NewReader(data)

	var headerLen uint64
	if err := binary.Read(f, binary.LittleEndian, &headerLen); err != nil {
		return fmt.Errorf("read header length: %w", err)
	}
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(hdr, &raw); err != nil {
		return fmt.Errorf("parse header JSON: %w", err)
	}

	dataStart := int64(8 + headerLen)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errCh := make(chan error, len(raw))

	for name, rv := range raw {
		if name == "__metadata__" {
			continue
		}
		wg.Add(1)
		go func(tensorName string, rawVal json.RawMessage) {
			defer wg.Done()
			var info tensorInfo
			if err := json.Unmarshal(rawVal, &info); err != nil {
				errCh <- fmt.Errorf("parse tensor %q: %w", tensorName, err)
				return
			}
			byteLen := info.DataOffsets[1] - info.DataOffsets[0]
			rawBytes := make([]byte, byteLen)
			offset := dataStart + int64(info.DataOffsets[0])
			if _, err := f.ReadAt(rawBytes, offset); err != nil {
				errCh <- fmt.Errorf("read tensor %q data: %w", tensorName, err)
				return
			}
			f32, err := bytesToFloat32(rawBytes, info.Dtype)
			if err != nil {
				errCh <- fmt.Errorf("convert tensor %q: %w", tensorName, err)
				return
			}
			mu.Lock()
			st.data[tensorName] = f32
			mu.Unlock()
		}(name, rv)
	}
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		return err
	}
	return nil
}

func bytesToFloat32(raw []byte, dtype string) ([]float32, error) {
	switch dtype {
	case "F32":
		n := len(raw) / 4
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		return out, nil
	case "BF16":
		n := len(raw) / 2
		out := make([]float32, n)
		for i := range out {
			u16 := binary.LittleEndian.Uint16(raw[i*2:])
			out[i] = math.Float32frombits(uint32(u16) << 16)
		}
		return out, nil
	case "F16":
		n := len(raw) / 2
		out := make([]float32, n)
		for i := range out {
			out[i] = f16ToF32(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported dtype %q", dtype)
	}
}

func f16ToF32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32((h >> 10) & 0x1F)
	mant := uint32(h & 0x3FF)
	var bits uint32
	switch {
	case exp == 0 && mant == 0:
		bits = sign
	case exp == 0:
		exp = 1
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		mant &= 0x3FF
		bits = sign | ((exp + 127 - 15) << 23) | (mant << 13)
	case exp == 31:
		bits = sign | 0x7F800000 | (mant << 13)
	default:
		bits = sign | ((exp + 127 - 15) << 23) | (mant << 13)
	}
	return math.Float32frombits(bits)
}

func loadModel(fs modelFS) (*qwen3Model, error) {
	st, err := loadSafetensorsDir(fs)
	if err != nil {
		return nil, err
	}

	get := func(name string) ([]float32, error) {
		if v, err := st.get(name); err == nil {
			return v, nil
		}
		baseName := name
		if strings.HasPrefix(name, "model.") {
			baseName = name[6:]
		}
		for k, val := range st.data {
			if k == baseName || strings.HasSuffix(k, "."+baseName) {
				return val, nil
			}
		}
		if name == "model.embed_tokens.weight" {
			for k, val := range st.data {
				if k == "lm_head.weight" || strings.HasSuffix(k, ".lm_head.weight") {
					return val, nil
				}
			}
		}
		var keys []string
		for k := range st.data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		limit := min(len(keys), 5)
		return nil, fmt.Errorf("missing weight %q\n[DEBUG] file contains keys like: %v", name, keys[:limit])
	}

	getOptional := func(name string) []float32 {
		v, err := st.get(name)
		if err == nil {
			return v
		}
		baseName := name
		if strings.HasPrefix(name, "model.") {
			baseName = name[6:]
		}
		for k, val := range st.data {
			if k == baseName || strings.HasSuffix(k, "."+baseName) {
				return val
			}
		}
		return nil
	}

	m := &qwen3Model{layers: make([]qwen3Layer, cfgNumLayers)}

	if m.embedTokens, err = get("model.embed_tokens.weight"); err != nil {
		return nil, err
	}
	if m.normWeight, err = get("model.norm.weight"); err != nil {
		return nil, err
	}

	for i := range m.layers {
		p := fmt.Sprintf("model.layers.%d.", i)
		lw := &m.layers[i]
		weights := []struct {
			dst  *[]float32
			name string
		}{
			{&lw.inputNorm, p + "input_layernorm.weight"},
			{&lw.qProj, p + "self_attn.q_proj.weight"},
			{&lw.kProj, p + "self_attn.k_proj.weight"},
			{&lw.vProj, p + "self_attn.v_proj.weight"},
			{&lw.oProj, p + "self_attn.o_proj.weight"},
			{&lw.qNorm, p + "self_attn.q_norm.weight"},
			{&lw.kNorm, p + "self_attn.k_norm.weight"},
			{&lw.postNorm, p + "post_attention_layernorm.weight"},
			{&lw.gateProj, p + "mlp.gate_proj.weight"},
			{&lw.upProj, p + "mlp.up_proj.weight"},
			{&lw.downProj, p + "mlp.down_proj.weight"},
		}
		for _, w := range weights {
			if *w.dst, err = get(w.name); err != nil {
				return nil, err
			}
		}
		lw.qBias = getOptional(p + "self_attn.q_proj.bias")
		lw.kBias = getOptional(p + "self_attn.k_proj.bias")
		lw.vBias = getOptional(p + "self_attn.v_proj.bias")
	}
	return m, nil
}

func (m *qwen3Model) embed(tokenIDs []int) []float32 {
	seqLen := len(tokenIDs)
	H := cfgHiddenSize

	hidden := make([]float32, seqLen*H)
	for i, id := range tokenIDs {
		copy(hidden[i*H:], m.embedTokens[id*H:(id+1)*H])
	}

	ropeCache := buildRoPECache(seqLen)

	for i := range m.layers {
		lw := &m.layers[i]
		normed := rmsNormRows(hidden, lw.inputNorm, seqLen)
		attn := selfAttention(normed, lw, ropeCache, seqLen)
		vecAdd(hidden, attn)
		normed2 := rmsNormRows(hidden, lw.postNorm, seqLen)
		mlpOut := swigluMLP(normed2, lw, seqLen)
		vecAdd(hidden, mlpOut)
	}

	last := make([]float32, H)
	copy(last, hidden[(seqLen-1)*H:])
	rmsNormVec(last, m.normWeight)
	l2Norm(last)
	return last
}

func selfAttention(x []float32, lw *qwen3Layer, ropeCache []float32, seqLen int) []float32 {
	H := cfgHiddenSize
	nH := cfgNumHeads
	nKV := cfgNumKVHeads
	hDim := cfgHeadDim

	q := matMul(x, lw.qProj, seqLen, H, nH*hDim)
	addBias(q, lw.qBias, seqLen, nH*hDim)
	k := matMul(x, lw.kProj, seqLen, H, nKV*hDim)
	addBias(k, lw.kBias, seqLen, nKV*hDim)
	v := matMul(x, lw.vProj, seqLen, H, nKV*hDim)
	addBias(v, lw.vBias, seqLen, nKV*hDim)

	rmsNormHeads(q, lw.qNorm, seqLen, nH, hDim)
	rmsNormHeads(k, lw.kNorm, seqLen, nKV, hDim)

	applyRoPE(q, ropeCache, seqLen, nH, hDim)
	applyRoPE(k, ropeCache, seqLen, nKV, hDim)

	attnOut := gqa(q, k, v, seqLen)
	return matMul(attnOut, lw.oProj, seqLen, nH*hDim, H)
}

func swigluMLP(x []float32, lw *qwen3Layer, seqLen int) []float32 {
	H := cfgHiddenSize
	I := cfgIntermediateSize
	gate := matMul(x, lw.gateProj, seqLen, H, I)
	up := matMul(x, lw.upProj, seqLen, H, I)
	for i := range gate {
		gate[i] = silu(gate[i]) * up[i]
	}
	return matMul(gate, lw.downProj, seqLen, I, H)
}
