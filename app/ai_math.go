// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"math"
	"runtime"
	"sync"
)

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func vecNorm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func addBias(x, bias []float32, rows, cols int) {
	if len(bias) == 0 {
		return
	}
	for i := range rows {
		for j := range cols {
			x[i*cols+j] += bias[j]
		}
	}
}

func gqa(q, k, v []float32, seqLen int) []float32 {
	nH := cfgNumHeads
	nKV := cfgNumKVHeads
	hDim := cfgHeadDim
	scale := float32(1.0 / math.Sqrt(float64(hDim)))
	groupSize := nH / nKV
	out := make([]float32, seqLen*nH*hDim)

	numWorkers := min(runtime.NumCPU(), nH)
	headCh := make(chan int, nH)
	for h := range nH {
		headCh <- h
	}
	close(headCh)

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for range numWorkers {
		go func() {
			defer wg.Done()
			scores := make([]float32, seqLen)
			for h := range headCh {
				kvH := h / groupSize
				for t := range seqLen {
					qOff := t*nH*hDim + h*hDim
					for s := 0; s <= t; s++ {
						kOff := s*nKV*hDim + kvH*hDim
						var d float32
						for i := range hDim {
							d += q[qOff+i] * k[kOff+i]
						}
						scores[s] = d * scale
					}
					softmax(scores[:t+1])
					oOff := t*nH*hDim + h*hDim
					for s := 0; s <= t; s++ {
						vOff := s*nKV*hDim + kvH*hDim
						sc := scores[s]
						for i := range hDim {
							out[oOff+i] += sc * v[vOff+i]
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	return out
}

func matMul(a, b []float32, rows, in, out int) []float32 {
	res := make([]float32, rows*out)
	numWorkers := min(runtime.NumCPU(), rows)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	rowsPerWorker := (rows + numWorkers - 1) / numWorkers
	const blockSize = 64

	for w := range numWorkers {
		go func(workerID int) {
			defer wg.Done()
			rStart := workerID * rowsPerWorker
			rEnd := min(rStart+rowsPerWorker, rows)
			for i := rStart; i < rEnd; i++ {
				aRow := a[i*in : (i+1)*in]
				resRow := res[i*out : (i+1)*out]
				for jBlock := 0; jBlock < out; jBlock += blockSize {
					jEnd := min(jBlock+blockSize, out)
					for j := jBlock; j < jEnd; j++ {
						bRow := b[j*in : (j+1)*in]
						var s float32 = 0
						k := 0
						for ; k < in-3; k += 4 {
							s += aRow[k]*bRow[k] +
								aRow[k+1]*bRow[k+1] +
								aRow[k+2]*bRow[k+2] +
								aRow[k+3]*bRow[k+3]
						}
						for ; k < in; k++ {
							s += aRow[k] * bRow[k]
						}
						resRow[j] = s
					}
				}
			}
		}(w)
	}
	wg.Wait()
	return res
}

func rmsNormRows(x, weight []float32, seqLen int) []float32 {
	H := cfgHiddenSize
	out := make([]float32, seqLen*H)
	for i := range seqLen {
		rmsNormSlice(x[i*H:(i+1)*H], out[i*H:(i+1)*H], weight, H)
	}
	return out
}

func rmsNormVec(x, weight []float32) {
	rmsNormSlice(x, x, weight, len(x))
}

func rmsNormSlice(x, out, w []float32, n int) {
	var ss float32
	for _, v := range x[:n] {
		ss += v * v
	}
	inv := float32(1.0 / math.Sqrt(float64(ss/float32(n))+float64(cfgRMSNormEps)))
	for i := range n {
		out[i] = x[i] * inv * w[i]
	}
}

func rmsNormHeads(x, weight []float32, seqLen, nHeads, headDim int) {
	for t := range seqLen {
		for h := range nHeads {
			base := t*nHeads*headDim + h*headDim
			rmsNormSlice(x[base:base+headDim], x[base:base+headDim], weight, headDim)
		}
	}
}

func buildRoPECache(seqLen int) []float32 {
	hDim := cfgHeadDim
	half := hDim / 2
	cache := make([]float32, seqLen*hDim)
	for pos := range seqLen {
		row := cache[pos*hDim:]
		for i := range half {
			freq := 1.0 / math.Pow(cfgRopeTheta, float64(2*i)/float64(hDim))
			angle := float64(pos) * freq
			row[i] = float32(math.Cos(angle))
			row[i+half] = float32(math.Sin(angle))
		}
	}
	return cache
}

func applyRoPE(x, cache []float32, seqLen, nHeads, headDim int) {
	half := headDim / 2
	for pos := range seqLen {
		cRow := cache[pos*headDim:]
		cos := cRow[:half]
		sin := cRow[half:]
		for h := range nHeads {
			base := pos*nHeads*headDim + h*headDim
			for i := range half {
				x0 := x[base+i]
				x1 := x[base+i+half]
				x[base+i] = x0*cos[i] - x1*sin[i]
				x[base+i+half] = x1*cos[i] + x0*sin[i]
			}
		}
	}
}

func softmax(x []float32) {
	max := x[0]
	for _, v := range x[1:] {
		if v > max {
			max = v
		}
	}
	var sum float32
	for i, v := range x {
		e := float32(math.Exp(float64(v - max)))
		x[i] = e
		sum += e
	}
	inv := 1.0 / sum
	for i := range x {
		x[i] *= inv
	}
}

func silu(x float32) float32 {
	return x / (1.0 + float32(math.Exp(float64(-x))))
}

func vecAdd(dst, src []float32) {
	for i := range dst {
		dst[i] += src[i]
	}
}

func l2Norm(x []float32) {
	var ss float32
	for _, v := range x {
		ss += v * v
	}
	if ss == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(float64(ss)))
	for i := range x {
		x[i] *= inv
	}
}
