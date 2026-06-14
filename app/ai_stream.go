// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

type GGUFStream struct {
	file   *os.File
	offset int64
}

func (s *GGUFStream) Read(p []byte) (n int, err error) {
	if s.offset >= globalBlobSize {
		return 0, io.EOF
	}

	usablePageBytes := globalPageSize - 4
	bytesToRead := int64(len(p))
	if s.offset+bytesToRead > globalBlobSize {
		bytesToRead = globalBlobSize - s.offset
	}

	totalRead := int64(0)
	for totalRead < bytesToRead {
		currentOffset := s.offset + totalRead
		pageIndex := currentOffset / usablePageBytes
		pageOffset := currentOffset % usablePageBytes

		if pageIndex >= int64(len(globalPages)) {
			break
		}

		physicalPage := globalPages[pageIndex]
		fileOffset := (int64(physicalPage)-1)*globalPageSize + 4 + pageOffset

		_, err = s.file.Seek(fileOffset, io.SeekStart)
		if err != nil {
			return int(totalRead), err
		}

		currentPageRemaining := usablePageBytes - pageOffset
		chunkSize := bytesToRead - totalRead
		if chunkSize > currentPageRemaining {
			chunkSize = currentPageRemaining
		}

		rn, err := io.ReadFull(s.file, p[totalRead:totalRead+chunkSize])
		totalRead += int64(rn)
		if err != nil {
			return int(totalRead), err
		}
	}

	s.offset += totalRead
	return int(totalRead), nil
}

func (s *GGUFStream) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = s.offset + offset
	case io.SeekEnd:
		newOffset = globalBlobSize + offset
	}
	if newOffset < 0 {
		return s.offset, fmt.Errorf("invalid seek offset")
	}
	s.offset = newOffset
	return s.offset, nil
}

type BufferedReadSeeker struct {
	r      io.ReadSeeker
	buf    []byte
	pos    int
	limit  int
	offset int64
}

func NewBufferedReadSeeker(r io.ReadSeeker, size int) *BufferedReadSeeker {
	return &BufferedReadSeeker{
		r:   r,
		buf: make([]byte, size),
	}
}

func (b *BufferedReadSeeker) Read(p []byte) (n int, err error) {
	if b.pos >= b.limit {
		b.pos = 0
		b.limit = 0
		rn, rerr := b.r.Read(b.buf)
		if rn > 0 {
			b.limit = rn
			b.offset += int64(rn)
		}
		if rerr != nil {
			if rerr == io.EOF && rn > 0 {
				rerr = nil
			} else {
				return 0, rerr
			}
		}
		if rn == 0 {
			return 0, io.EOF
		}
	}

	n = copy(p, b.buf[b.pos:b.limit])
	b.pos += n
	return n, nil
}

func (b *BufferedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent && offset == 0 {
		return b.offset - int64(b.limit-b.pos), nil
	}

	b.pos = 0
	b.limit = 0
	newOff, err := b.r.Seek(offset, whence)
	if err == nil {
		b.offset = newOff
	}
	return newOff, err
}

func readVarint(buf []byte, offset int) (int64, int) {
	var val int64
	for i := 0; i < 9; i++ {
		b := buf[offset+i]
		if i == 8 {
			val = (val << 8) | int64(b)
			return val, i + 1
		}
		val = (val << 7) | int64(b&0x7F)
		if (b & 0x80) == 0 {
			return val, i + 1
		}
	}
	return val, 9
}

func parseRecord(payload []byte) []any {
	if len(payload) == 0 {
		return nil
	}
	headerSize, n := readVarint(payload, 0)
	pos := n
	var serialTypes []int64
	for int64(pos) < headerSize {
		st, sn := readVarint(payload, pos)
		serialTypes = append(serialTypes, st)
		pos += sn
	}

	cols := make([]any, len(serialTypes))
	dataPos := int(headerSize)

	for i, st := range serialTypes {
		if dataPos > len(payload) {
			dataPos = len(payload)
		}
		switch {
		case st == 0:
			cols[i] = nil
		case st >= 1 && st <= 6:
			sizes := []int{0, 1, 2, 3, 4, 6, 8}
			size := sizes[st]
			if dataPos+size > len(payload) {
				size = len(payload) - dataPos
			}
			var val int64
			for j := 0; j < size; j++ {
				val = (val << 8) | int64(payload[dataPos+j])
			}
			cols[i] = val
			dataPos += size
		case st == 7:
			size := 8
			if dataPos+size > len(payload) {
				size = len(payload) - dataPos
			}
			var val int64
			for j := 0; j < size; j++ {
				val = (val << 8) | int64(payload[dataPos+j])
			}
			cols[i] = math.Float64frombits(uint64(val))
			dataPos += 8
		case st == 8:
			cols[i] = int64(0)
		case st == 9:
			cols[i] = int64(1)
		case st >= 12 && st%2 == 0:
			length := int((st - 12) / 2)
			end := dataPos + length
			if end > len(payload) {
				end = len(payload)
			}
			cols[i] = payload[dataPos:end]
			dataPos += length
		case st >= 13 && st%2 != 0:
			length := int((st - 13) / 2)
			end := dataPos + length
			if end > len(payload) {
				end = len(payload)
			}
			cols[i] = string(payload[dataPos:end])
			dataPos += length
		}
	}
	return cols
}

func parseSetupCell(cellBuf []byte, pageSize int64) (string, uint32, int64) {
	pos := 0
	payloadSize, n := readVarint(cellBuf, pos)
	pos += n
	_, n = readVarint(cellBuf, pos)
	pos += n

	cellHeaderOffset := pos

	P := payloadSize
	U := pageSize
	X := U - 35
	M := ((U - 12) * 32 / 255) - 23
	K := M + ((P - M) % (U - 12))

	var localPayload int64
	if P <= X {
		localPayload = P
	} else if K <= X {
		localPayload = K
	} else {
		localPayload = M
	}

	localBytes := cellBuf[cellHeaderOffset : cellHeaderOffset+int(localPayload)]

	cols := parseRecord(localBytes)
	if len(cols) >= 2 {
		key, ok := cols[0].(string)
		if ok && key == "gguf" {
			var firstOverflowPage uint32
			if P > localPayload {
				offPageOffset := cellHeaderOffset + int(localPayload)
				firstOverflowPage = binary.BigEndian.Uint32(cellBuf[offPageOffset : offPageOffset+4])
			}
			return key, firstOverflowPage, P
		}
	}
	return "", 0, 0
}

func readPage(file *os.File, pageNum int64, pageSize int64) ([]byte, error) {
	buf := make([]byte, pageSize)
	_, err := file.ReadAt(buf, (pageNum-1)*pageSize)
	return buf, err
}

func findSetupRootPage(file *os.File, pageSize int64) (int64, error) {
	page1, err := readPage(file, 1, pageSize)
	if err != nil {
		return 0, err
	}

	pageHeaderOff := 100
	numCells := int(binary.BigEndian.Uint16(page1[pageHeaderOff+3 : pageHeaderOff+5]))
	cellPtrsOff := pageHeaderOff + 8

	for i := 0; i < numCells; i++ {
		cellPtr := int(binary.BigEndian.Uint16(page1[cellPtrsOff+i*2 : cellPtrsOff+i*2+2]))
		cellBuf := page1[cellPtr:]

		pos := 0
		payloadSize, n := readVarint(cellBuf, pos)
		pos += n
		_, n = readVarint(cellBuf, pos)
		pos += n

		P := payloadSize
		U := pageSize
		X := U - 35
		M := ((U - 12) * 32 / 255) - 23
		K := M + ((P - M) % (U - 12))

		var localPayload int64
		if P <= X {
			localPayload = P
		} else if K <= X {
			localPayload = K
		} else {
			localPayload = M
		}

		localBytes := cellBuf[pos : pos+int(localPayload)]
		cols := parseRecord(localBytes)
		if len(cols) >= 4 {
			tblName, ok2 := cols[2].(string)
			rootPage, ok3 := cols[3].(int64)
			if ok2 && ok3 && tblName == "setup" {
				return rootPage, nil
			}
		}
	}

	return 0, fmt.Errorf("setup table not found in schema")
}

func findGGUFCell(file *os.File, pageNum int64, pageSize int64) (uint32, int64, error) {
	buf, err := readPage(file, pageNum, pageSize)
	if err != nil {
		return 0, 0, err
	}

	pageType := buf[0]
	if pageType == 0x0d {
		numCells := int(binary.BigEndian.Uint16(buf[3:5]))
		cellPtrsOff := 8
		for i := 0; i < numCells; i++ {
			cellPtr := int(binary.BigEndian.Uint16(buf[cellPtrsOff+i*2 : cellPtrsOff+i*2+2]))
			cellBuf := buf[cellPtr:]
			key, firstOverflowPage, blobSize := parseSetupCell(cellBuf, pageSize)
			if key == "gguf" {
				return firstOverflowPage, blobSize, nil
			}
		}
	} else if pageType == 0x05 {
		numCells := int(binary.BigEndian.Uint16(buf[3:5]))
		cellPtrsOff := 12
		for i := 0; i < numCells; i++ {
			cellPtr := int(binary.BigEndian.Uint16(buf[cellPtrsOff+i*2 : cellPtrsOff+i*2+2]))
			childPage := int64(binary.BigEndian.Uint32(buf[cellPtr : cellPtr+4]))
			firstOverflowPage, blobSize, err := findGGUFCell(file, childPage, pageSize)
			if err == nil {
				return firstOverflowPage, blobSize, nil
			}
		}
		rightmostPage := int64(binary.BigEndian.Uint32(buf[8:12]))
		return findGGUFCell(file, rightmostPage, pageSize)
	}

	return 0, 0, fmt.Errorf("gguf cell not found")
}

func mapOverflowPages(file *os.File, firstPage uint32, pageSize int64) ([]uint32, error) {
	var pages []uint32
	currentPage := firstPage
	buf := make([]byte, 4)

	for currentPage != 0 {
		pages = append(pages, currentPage)
		_, err := file.ReadAt(buf, (int64(currentPage)-1)*pageSize)
		if err != nil {
			return nil, err
		}
		currentPage = binary.BigEndian.Uint32(buf)
	}

	return pages, nil
}

func openGGUFStream() (io.ReadSeeker, func() error, error) {
	if globalUseCustomStream {
		f, err := os.Open(options.dbPath)
		if err == nil {
			return &GGUFStream{file: f}, f.Close, nil
		}
	}

	r, closeFn, err := db.AiModelBlob()
	if err == nil {
		return r, closeFn, nil
	}

	if options.aiModel != "" {
		f, err := os.Open(options.aiModel)
		if err == nil {
			return f, f.Close, nil
		}
	}

	return nil, nil, fmt.Errorf("no GGUF model source available")
}
