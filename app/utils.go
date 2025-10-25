// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"bytes"
	"compress/flate"
	"crypto/md5"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

//go:embed assets
var assets embed.FS

func calculateHash(texts []string) string {
	hasher := md5.New()
	for _, text := range texts {
		hasher.Write([]byte(text))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func cleanHashes(hashes []string) []string {
	cleanedHashes := make([]string, len(hashes))

	for i, hash := range hashes {
		cleanedHash := strings.ToLower(strings.ReplaceAll(hash, "-", ""))
		cleanedHashes[i] = cleanedHash
	}

	return cleanedHashes
}

func extractNumberFromString(s string) int {
	re := regexp.MustCompile(`\d+`)
	match := re.FindString(s)
	if match != "" {
		num, err := strconv.Atoi(match)
		if err != nil {
			return 0
		}
		return num
	}
	return 0
}

func TextInflate(data []byte) string {
	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	var out bytes.Buffer
	_, err := io.Copy(&out, reader)
	if err != nil {
		return ""
	}

	return out.String()
}

func TextDeflate(text string) []byte {
	var out bytes.Buffer

	writer, err := flate.NewWriter(&out, flate.DefaultCompression)
	if err != nil {
		return nil
	}
	defer writer.Close()

	_, err = writer.Write([]byte(text))
	if err != nil {
		return nil
	}

	err = writer.Close()
	if err != nil {
		return nil
	}

	return out.Bytes()
}

func MuteStderr() (*os.File, error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to mute stderr: %v", err)
	}
	return devNull, nil
}

type byteCounter struct {
	total *int64
}

func (bc *byteCounter) Write(p []byte) (int, error) {
	*bc.total += int64(len(p))
	return len(p), nil
}

func QuantizeBinary(values []float32) []byte {
	numBytes := (len(values) + 7) / 8
	packedData := make([]byte, numBytes)

	for i, value := range values {
		if value >= 0 {
			packedData[i/8] |= 1 << (i % 8)
		}
	}

	return packedData
}

func BytesToFloat32(bytes []byte) []float32 {
	if len(bytes)%4 != 0 {
		panic("input byte slice length must be a multiple of 4")
	}

	float32s := make([]float32, 0, len(bytes)/4)
	for i := 0; i < len(bytes); i += 4 {
		bits := binary.LittleEndian.Uint32(bytes[i : i+4])
		float32s = append(float32s, math.Float32frombits(bits))
	}

	return float32s
}

func Float32ToBytes(values []float32) []byte {
	bytes := make([]byte, 4*len(values))

	for i, value := range values {
		bits := math.Float32bits(value)
		binary.LittleEndian.PutUint32(bytes[4*i:4*(i+1)], bits)
	}

	return bytes
}

func NormalizeVectors(vectors [][]float32) [][]float32 {
	normalized := make([][]float32, len(vectors))

	for i, vec := range vectors {
		if len(vec) == 0 {
			normalized[i] = vec
			continue
		}

		magnitude := float32(0.0)
		for _, val := range vec {
			magnitude += val * val
		}
		magnitude = float32(math.Sqrt(float64(magnitude)))

		if magnitude == 0 {
			normalized[i] = make([]float32, len(vec))
			copy(normalized[i], vec)
		} else {
			normalized[i] = make([]float32, len(vec))
			for j, val := range vec {
				normalized[i][j] = val / magnitude
			}
		}
	}

	return normalized
}

func ExtractMRL(embedding []float32, size int) []byte {
	if size <= 0 || size > len(embedding) {
		size = len(embedding)
	}

	normalized := NormalizeVectors([][]float32{embedding})[0]

	result := make([]byte, size*4)
	for i := 0; i < size; i++ {
		bits := math.Float32bits(normalized[i])
		binary.LittleEndian.PutUint32(result[i*4:(i+1)*4], bits)
	}

	return result
}
