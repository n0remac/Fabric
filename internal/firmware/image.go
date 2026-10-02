package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	MinImageBytes int64 = 64 << 10
	MaxImageBytes int64 = 0x640000
	ImageChipID         = 5 // ESP32-C3; CrossPoint's combined X3/X4 image.
	BoardTag            = "x4"
)

// ValidateImage accepts one raw OTA application, not a bootloader or merged flash image.
func ValidateImage(f *os.File) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < MinImageBytes || stat.Size() > MaxImageBytes {
		return errors.New("image does not fit the X3 OTA partition")
	}
	var header [24]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return err
	}
	if header[0] != 0xe9 || header[1] == 0 || header[1] > 16 || header[23] > 1 {
		return errors.New("invalid ESP application header")
	}
	if binary.LittleEndian.Uint16(header[12:14]) != ImageChipID {
		return errors.New("image is not for ESP32-C3")
	}
	var descriptor [4]byte
	if _, err := f.ReadAt(descriptor[:], 32); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(descriptor[:]) != 0xabcd5432 {
		return errors.New("image has no ESP application descriptor")
	}
	pos := int64(24)
	checksum := byte(0xef)
	buffer := make([]byte, 32<<10)
	tags := tagScanner{}
	for segment := 0; segment < int(header[1]); segment++ {
		var sh [8]byte
		if _, err := f.ReadAt(sh[:], pos); err != nil {
			return errors.New("truncated segment header")
		}
		pos += 8
		length := int64(binary.LittleEndian.Uint32(sh[4:]))
		if length > stat.Size()-pos || (segment == 0 && length < 256) {
			return errors.New("invalid segment length")
		}
		tags.reset()
		for remaining := length; remaining > 0; {
			n := min(remaining, int64(len(buffer)))
			if _, err := f.ReadAt(buffer[:n], pos); err != nil {
				return err
			}
			for _, b := range buffer[:n] {
				checksum ^= b
			}
			if err := tags.feed(buffer[:n]); err != nil {
				return err
			}
			pos += n
			remaining -= n
		}
	}
	if !tags.found {
		return errors.New("image has no CrossPoint X3/X4 board tag")
	}
	padEnd := (pos + 16) &^ 15
	expected := padEnd
	if header[23] == 1 {
		expected += sha256.Size
	}
	if expected != stat.Size() {
		return errors.New("image has missing or trailing bytes")
	}
	var padding [16]byte
	if _, err := f.ReadAt(padding[:padEnd-pos], pos); err != nil {
		return err
	}
	if padding[padEnd-pos-1] != checksum {
		return errors.New("image checksum mismatch")
	}
	for _, b := range padding[:padEnd-pos-1] {
		if b != 0 {
			return errors.New("invalid image padding")
		}
	}
	if header[23] == 1 {
		h := sha256.New()
		if _, err := io.Copy(h, io.NewSectionReader(f, 0, padEnd)); err != nil {
			return err
		}
		var digest [sha256.Size]byte
		if _, err := f.ReadAt(digest[:], padEnd); err != nil {
			return err
		}
		if !bytes.Equal(h.Sum(nil), digest[:]) {
			return errors.New("image appended SHA256 mismatch")
		}
	}
	return nil
}

type tagScanner struct {
	matched   int
	capturing bool
	name      []byte
	found     bool
}

func (s *tagScanner) reset() { s.matched = 0; s.capturing = false; s.name = s.name[:0] }
func (s *tagScanner) feed(data []byte) error {
	const prefix = "CROSSPOINT-BOARD-V1:"
	for _, b := range data {
		if s.capturing {
			if b == ';' {
				if string(s.name) != BoardTag {
					return fmt.Errorf("incompatible CrossPoint board tag %q", s.name)
				}
				s.found = true
				s.reset()
			} else if len(s.name) < 23 && b > 0x20 && b < 0x7f {
				s.name = append(s.name, b)
			} else {
				s.reset()
			}
			continue
		}
		if b == prefix[s.matched] {
			s.matched++
			if s.matched == len(prefix) {
				s.matched = 0
				s.capturing = true
				s.name = s.name[:0]
			}
		} else {
			s.matched = 0
			if b == prefix[0] {
				s.matched = 1
			}
		}
	}
	return nil
}
