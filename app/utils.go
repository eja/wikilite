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
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
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

func TextInflate(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}

	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	var out bytes.Buffer
	_, err := io.Copy(&out, reader)
	if err != nil {
		return "", fmt.Errorf("decompression failed: %w", err)
	}

	return out.String(), nil
}

func TextDeflate(text string) ([]byte, error) {
	if text == "" {
		return []byte{}, nil
	}

	var out bytes.Buffer
	writer, err := flate.NewWriter(&out, flate.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("compression init failed: %w", err)
	}

	_, err = writer.Write([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("compression write failed: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("compression finalize failed: %w", err)
	}

	return out.Bytes(), nil
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

	truncated := make([]float32, size)
	copy(truncated, embedding[:size])
	l2Norm(truncated)

	result := make([]byte, size*4)
	for i := 0; i < size; i++ {
		bits := math.Float32bits(truncated[i])
		binary.LittleEndian.PutUint32(result[i*4:(i+1)*4], bits)
	}

	return result
}

func OpenBrowser(url string, delay int) error {
	var cmd string
	var args []string

	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "android":
		cmd = "am"
		args = []string{"start", "-a", "android.intent.action.VIEW", "-d", url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	}

	if cmd == "" {
		return fmt.Errorf("Unsupported OS")
	}
	return exec.Command(cmd, args...).Start()
}

func ReadLine(prompt string) string {
	if prompt != "" {
		fmt.Print(prompt)
	}

	var input string
	fmt.Scanln(&input)
	return input
}
