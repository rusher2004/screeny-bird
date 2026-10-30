package catalog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/png"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// validatePNG checks that data is a PNG of exactly the given size that carries
// no metadata. Only the critical chunks IHDR, PLTE, IDAT and IEND are allowed:
// text, color-profile, physical-size and every other ancillary chunk can carry
// information a public catalog must not. Every chunk's CRC is verified.
func validatePNG(data []byte, width, height int) error {
	if !bytes.HasPrefix(data, pngSignature) {
		return fmt.Errorf("not a PNG: %w", ErrInvalidImage)
	}

	rest := data[len(pngSignature):]
	first, sawData, sawEnd := true, false, false

	for len(rest) > 0 {
		if sawEnd {
			return fmt.Errorf("data after the IEND chunk: %w", ErrInvalidImage)
		}

		if len(rest) < 12 {
			return fmt.Errorf("truncated chunk header: %w", ErrInvalidImage)
		}

		length := int64(binary.BigEndian.Uint32(rest[:4]))
		if length > int64(len(rest))-12 {
			return fmt.Errorf("chunk length %d runs past the end of the file: %w", length, ErrInvalidImage)
		}

		kind := string(rest[4:8])
		body := rest[8 : 8+length]
		crc := binary.BigEndian.Uint32(rest[8+length : 12+length])

		if crc32.ChecksumIEEE(rest[4:8+length]) != crc {
			return fmt.Errorf("%s chunk has a bad checksum: %w", kind, ErrInvalidImage)
		}

		if first {
			if kind != "IHDR" || length != 13 {
				return fmt.Errorf("first chunk is %s, not IHDR: %w", kind, ErrInvalidImage)
			}

			gotWidth, gotHeight := binary.BigEndian.Uint32(body[:4]), binary.BigEndian.Uint32(body[4:8])
			if int64(gotWidth) != int64(width) || int64(gotHeight) != int64(height) {
				return fmt.Errorf("image is %dx%d, want %dx%d: %w", gotWidth, gotHeight, width, height, ErrInvalidImage)
			}

			first = false
		}

		switch kind {
		case "IHDR", "PLTE":
		case "IDAT":
			sawData = true
		case "IEND":
			sawEnd = true
		default:
			return fmt.Errorf("%s chunk is not allowed: images must carry no metadata: %w", kind, ErrInvalidImage)
		}

		rest = rest[12+length:]
	}

	if !sawData || !sawEnd {
		return fmt.Errorf("image is incomplete: %w", ErrInvalidImage)
	}

	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("decoding image header: %w: %w", ErrInvalidImage, err)
	}

	return nil
}
