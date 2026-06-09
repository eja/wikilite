// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"fmt"
)

var (
	cfgVocabSize        = 151669
	cfgHiddenSize       = 1024
	cfgNumLayers        = 28
	cfgNumHeads         = 16
	cfgNumKVHeads       = 8
	cfgHeadDim          = 128
	cfgIntermediateSize = 3072
	cfgRopeTheta        = 1000000.0
	cfgRMSNormEps       = float32(1e-6)
)

type qwen3Model struct {
	embedTokens []float32
	normWeight  []float32
	layers      []qwen3Layer
}

type qwen3Layer struct {
	inputNorm []float32
	qProj     Tensor
	qBias     []float32
	kProj     Tensor
	kBias     []float32
	vProj     Tensor
	vBias     []float32
	oProj     Tensor
	qNorm     []float32
	kNorm     []float32
	postNorm  []float32
	gateProj  Tensor
	upProj    Tensor
	downProj  Tensor
}

func loadModel(p *GGUFParser) (*qwen3Model, error) {
	archRaw, ok := p.Metadata["general.architecture"]
	if !ok {
		return nil, fmt.Errorf("missing model architecture metadata")
	}
	arch, ok := archRaw.(string)
	if !ok {
		return nil, fmt.Errorf("invalid architecture metadata type")
	}

	if arch != "qwen2" && arch != "qwen3" {
		return nil, fmt.Errorf("local execution only supports Qwen3 models")
	}

	tInfo, ok := p.Tensors["blk.0.attn_q.weight"]
	if !ok || tInfo.Type != 8 {
		return nil, fmt.Errorf("local execution only supports Q8.0 format")
	}

	getUint32 := func(suffix string, fallback int) int {
		if val, ok := p.Metadata[arch+"."+suffix]; ok {
			switch v := val.(type) {
			case uint32:
				return int(v)
			case uint64:
				return int(v)
			case int32:
				return int(v)
			case int64:
				return int(v)
			}
		}
		return fallback
	}

	getFloat32 := func(suffix string, fallback float32) float32 {
		if val, ok := p.Metadata[arch+"."+suffix]; ok {
			switch v := val.(type) {
			case float32:
				return v
			case float64:
				return float32(v)
			}
		}
		return fallback
	}

	getFloat64 := func(suffix string, fallback float64) float64 {
		if val, ok := p.Metadata[arch+"."+suffix]; ok {
			switch v := val.(type) {
			case float32:
				return float64(v)
			case float64:
				return v
			}
		}
		return fallback
	}

	cfgNumLayers = getUint32("block_count", cfgNumLayers)
	cfgHiddenSize = getUint32("embedding_length", cfgHiddenSize)
	cfgNumHeads = getUint32("attention.head_count", cfgNumHeads)
	cfgNumKVHeads = getUint32("attention.head_count_kv", cfgNumKVHeads)
	cfgHeadDim = getUint32("attention.head_dim", cfgHeadDim)
	if cfgHeadDim == 0 && cfgNumHeads > 0 {
		cfgHeadDim = cfgHiddenSize / cfgNumHeads
	}
	cfgIntermediateSize = getUint32("feed_forward_length", cfgIntermediateSize)
	cfgRopeTheta = getFloat64("rope.freq_base", cfgRopeTheta)
	cfgRMSNormEps = getFloat32("attention.layer_norm_rms_epsilon", cfgRMSNormEps)

	m := &qwen3Model{layers: make([]qwen3Layer, cfgNumLayers)}

	var err error

	if m.embedTokens, err = p.GetTensorF32("token_embd.weight"); err != nil {
		return nil, err
	}

	if m.normWeight, err = p.GetTensorF32("output_norm.weight"); err != nil {
		return nil, err
	}

	for i := range m.layers {
		lw := &m.layers[i]

		if lw.inputNorm, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_norm.weight", i)); err != nil {
			return nil, err
		}

		if lw.qProj, err = p.GetTensor(fmt.Sprintf("blk.%d.attn_q.weight", i)); err != nil {
			return nil, err
		}
		if lw.kProj, err = p.GetTensor(fmt.Sprintf("blk.%d.attn_k.weight", i)); err != nil {
			return nil, err
		}
		if lw.vProj, err = p.GetTensor(fmt.Sprintf("blk.%d.attn_v.weight", i)); err != nil {
			return nil, err
		}
		if lw.oProj, err = p.GetTensor(fmt.Sprintf("blk.%d.attn_output.weight", i)); err != nil {
			return nil, err
		}

		if lw.qNorm, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_q_norm.weight", i)); err != nil {
			lw.qNorm = nil
		}
		if lw.kNorm, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_k_norm.weight", i)); err != nil {
			lw.kNorm = nil
		}

		if lw.postNorm, err = p.GetTensorF32(fmt.Sprintf("blk.%d.ffn_norm.weight", i)); err != nil {
			return nil, err
		}

		if lw.gateProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_gate.weight", i)); err != nil {
			return nil, err
		}
		if lw.upProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_up.weight", i)); err != nil {
			return nil, err
		}
		if lw.downProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_down.weight", i)); err != nil {
			return nil, err
		}

		if lw.qBias, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_q.bias", i)); err != nil {
			lw.qBias = nil
		}
		if lw.kBias, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_k.bias", i)); err != nil {
			lw.kBias = nil
		}
		if lw.vBias, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_v.bias", i)); err != nil {
			lw.vBias = nil
		}
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

	q := matMulQuant(x, lw.qProj, seqLen, H, nH*hDim)
	addBias(q, lw.qBias, seqLen, nH*hDim)
	k := matMulQuant(x, lw.kProj, seqLen, H, nKV*hDim)
	addBias(k, lw.kBias, seqLen, nKV*hDim)
	v := matMulQuant(x, lw.vProj, seqLen, H, nKV*hDim)
	addBias(v, lw.vBias, seqLen, nKV*hDim)

	rmsNormHeads(q, lw.qNorm, seqLen, nH, hDim)
	rmsNormHeads(k, lw.kNorm, seqLen, nKV, hDim)

	applyRoPE(q, ropeCache, seqLen, nH, hDim)
	applyRoPE(k, ropeCache, seqLen, nKV, hDim)

	attnOut := gqa(q, k, v, seqLen)
	return matMulQuant(attnOut, lw.oProj, seqLen, nH*hDim, H)
}

func swigluMLP(x []float32, lw *qwen3Layer, seqLen int) []float32 {
	H := cfgHiddenSize
	I := cfgIntermediateSize
	gate := matMulQuant(x, lw.gateProj, seqLen, H, I)
	up := matMulQuant(x, lw.upProj, seqLen, H, I)
	for i := range gate {
		gate[i] = silu(gate[i]) * up[i]
	}
	return matMulQuant(gate, lw.downProj, seqLen, I, H)
}
