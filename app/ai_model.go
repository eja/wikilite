// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"fmt"
)

var (
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
	parser      *GGUFParser
}

type qwen3Layer struct {
	idx       int
	parser    *GGUFParser
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

	m := &qwen3Model{
		layers: make([]qwen3Layer, cfgNumLayers),
		parser: p,
	}

	var err error

	if options.aiCache {
		if m.embedTokens, err = p.GetTensorF32("token_embd.weight"); err != nil {
			return nil, err
		}

		if m.normWeight, err = p.GetTensorF32("output_norm.weight"); err != nil {
			return nil, err
		}
	}

	for i := range m.layers {
		lw := &m.layers[i]
		lw.idx = i
		lw.parser = p

		if lw.inputNorm, err = p.GetTensorF32(fmt.Sprintf("blk.%d.attn_norm.weight", i)); err != nil {
			return nil, err
		}

		if options.aiCache {
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

		if options.aiCache {
			if lw.gateProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_gate.weight", i)); err != nil {
				return nil, err
			}
			if lw.upProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_up.weight", i)); err != nil {
				return nil, err
			}
			if lw.downProj, err = p.GetTensor(fmt.Sprintf("blk.%d.ffn_down.weight", i)); err != nil {
				return nil, err
			}
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

func (m *qwen3Model) embed(tokenIDs []int) ([]float32, error) {
	seqLen := len(tokenIDs)
	H := cfgHiddenSize

	hidden := make([]float32, seqLen*H)
	if !options.aiCache {
		for i, id := range tokenIDs {
			emb, err := m.parser.GetTokenEmbedding(id, H)
			if err != nil {
				return nil, fmt.Errorf("failed to get token embedding for ID %d: %w", id, err)
			}
			copy(hidden[i*H:], emb)
		}
	} else {
		for i, id := range tokenIDs {
			copy(hidden[i*H:], m.embedTokens[id*H:(id+1)*H])
		}
	}

	ropeCache := buildRoPECache(seqLen)

	for i := range m.layers {
		lw := &m.layers[i]
		normed := rmsNormRows(hidden, lw.inputNorm, seqLen)
		attn := selfAttention(normed, lw, ropeCache, seqLen)
		if attn == nil {
			return nil, fmt.Errorf("selfAttention failed at layer %d", i)
		}
		vecAdd(hidden, attn)
		normed2 := rmsNormRows(hidden, lw.postNorm, seqLen)
		mlpOut := swigluMLP(normed2, lw, seqLen)
		if mlpOut == nil {
			return nil, fmt.Errorf("swigluMLP failed at layer %d", i)
		}
		vecAdd(hidden, mlpOut)
	}

	last := make([]float32, H)
	copy(last, hidden[(seqLen-1)*H:])

	var normWeight []float32
	var err error
	if !options.aiCache {
		normWeight, err = m.parser.GetTensorF32("output_norm.weight")
		if err != nil {
			return nil, err
		}
	} else {
		normWeight = m.normWeight
	}

	rmsNormVec(last, normWeight)
	l2Norm(last)
	return last, nil
}

func selfAttention(x []float32, lw *qwen3Layer, ropeCache []float32, seqLen int) []float32 {
	H := cfgHiddenSize
	nH := cfgNumHeads
	nKV := cfgNumKVHeads
	hDim := cfgHeadDim

	var qProj, kProj, vProj, oProj Tensor
	var err error

	if !options.aiCache {
		qProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.attn_q.weight", lw.idx))
		if err != nil {
			return nil
		}
		kProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.attn_k.weight", lw.idx))
		if err != nil {
			return nil
		}
		vProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.attn_v.weight", lw.idx))
		if err != nil {
			return nil
		}
	} else {
		qProj = lw.qProj
		kProj = lw.kProj
		vProj = lw.vProj
	}

	q := matMulQuant(x, qProj, seqLen, H, nH*hDim)
	addBias(q, lw.qBias, seqLen, nH*hDim)
	k := matMulQuant(x, kProj, seqLen, H, nKV*hDim)
	addBias(k, lw.kBias, seqLen, nKV*hDim)
	v := matMulQuant(x, vProj, seqLen, H, nKV*hDim)
	addBias(v, lw.vBias, seqLen, nKV*hDim)

	rmsNormHeads(q, lw.qNorm, seqLen, nH, hDim)
	rmsNormHeads(k, lw.kNorm, seqLen, nKV, hDim)

	applyRoPE(q, ropeCache, seqLen, nH, hDim)
	applyRoPE(k, ropeCache, seqLen, nKV, hDim)

	attnOut := gqa(q, k, v, seqLen)

	if !options.aiCache {
		oProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.attn_output.weight", lw.idx))
		if err != nil {
			return nil
		}
	} else {
		oProj = lw.oProj
	}

	return matMulQuant(attnOut, oProj, seqLen, nH*hDim, H)
}

func swigluMLP(x []float32, lw *qwen3Layer, seqLen int) []float32 {
	H := cfgHiddenSize
	I := cfgIntermediateSize

	var gateProj, upProj, downProj Tensor
	var err error

	if !options.aiCache {
		gateProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.ffn_gate.weight", lw.idx))
		if err != nil {
			return nil
		}
		upProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.ffn_up.weight", lw.idx))
		if err != nil {
			return nil
		}
	} else {
		gateProj = lw.gateProj
		upProj = lw.upProj
	}

	gate := matMulQuant(x, gateProj, seqLen, H, I)
	up := matMulQuant(x, upProj, seqLen, H, I)
	for i := range gate {
		gate[i] = silu(gate[i]) * up[i]
	}

	if !options.aiCache {
		downProj, err = lw.parser.GetTensor(fmt.Sprintf("blk.%d.ffn_down.weight", lw.idx))
		if err != nil {
			return nil
		}
	} else {
		downProj = lw.downProj
	}

	return matMulQuant(gate, downProj, seqLen, I, H)
}
