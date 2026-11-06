package archive

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/JeropsWang/local-project-hub/internal/domain/bundle"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

// checkDirectory bounds actual central directory records before archive/zip
// builds its in-memory index. The v1 writer emits a single-disk, non-ZIP64 ZIP.
func checkDirectory(source io.ReaderAt, size int64) error {
	if size < 22 || size > bundle.MaxArchive {
		return problem.New("BUNDLE_LIMIT", "")
	}
	tail := make([]byte, min(size, 65535+22))
	if _, err := source.ReadAt(tail, size-int64(len(tail))); err != nil {
		return err
	}
	for end := len(tail) - 22; end >= 0; end-- {
		header := tail[end:]
		if !bytes.Equal(header[:4], []byte("PK\x05\x06")) ||
			int(binary.LittleEndian.Uint16(header[20:22])) != len(header)-22 {
			continue
		}
		count := int(binary.LittleEndian.Uint16(header[10:12]))
		length := int64(binary.LittleEndian.Uint32(header[12:16]))
		offset := int64(binary.LittleEndian.Uint32(header[16:20]))
		if count > bundle.MaxFiles+1 {
			return problem.New("BUNDLE_LIMIT", "")
		}
		if binary.LittleEndian.Uint32(header[4:8]) != 0 || int(binary.LittleEndian.Uint16(header[8:10])) != count ||
			offset+length != size-int64(len(header)) {
			break
		}
		var fixed [46]byte
		for index := 0; offset < size-int64(len(header)); index++ {
			if index >= count || offset+46 > size-int64(len(header)) {
				return problem.New("BUNDLE_ZIP_INVALID", "")
			}
			if _, err := source.ReadAt(fixed[:], offset); err != nil {
				return err
			}
			if !bytes.Equal(fixed[:4], []byte("PK\x01\x02")) {
				return problem.New("BUNDLE_ZIP_INVALID", "")
			}
			offset += 46 + int64(binary.LittleEndian.Uint16(fixed[28:30])) +
				int64(binary.LittleEndian.Uint16(fixed[30:32])) + int64(binary.LittleEndian.Uint16(fixed[32:34]))
			if index+1 == count && offset == size-int64(len(header)) {
				return nil
			}
		}
		break
	}
	return problem.New("BUNDLE_ZIP_INVALID", "")
}
