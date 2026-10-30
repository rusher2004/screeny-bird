package catalog_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

// pngChunk encodes one PNG chunk with a correct CRC.
func pngChunk(kind string, data []byte) []byte {
	var buf bytes.Buffer

	// The writes target a bytes.Buffer, which never fails.
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.WriteString(kind)
	buf.Write(data)

	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))

	return buf.Bytes()
}

// withChunkBeforeIDAT inserts an extra chunk ahead of the first IDAT chunk.
func withChunkBeforeIDAT(t *testing.T, src []byte, chunk []byte) []byte {
	t.Helper()

	at := bytes.Index(src, []byte("IDAT"))
	if at < 4 {
		t.Fatal("fixture PNG has no IDAT chunk")
	}

	insert := at - 4

	return append(append(append([]byte{}, src[:insert]...), chunk...), src[insert:]...)
}

func TestLoadRejectsBadImages(t *testing.T) {
	t.Parallel()

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatalf("encoding JPEG fixture: %v", err)
	}

	corrupt := bytes.Clone(portraitPNG)
	corrupt[len(corrupt)/2] ^= 0xFF

	tests := []struct {
		name     string
		portrait []byte
		display  []byte
	}{
		{name: "portrait has the display dimensions", portrait: displayPNG},
		{name: "display has the portrait dimensions", display: portraitPNG},
		{name: "wrong size", portrait: mustPNG(1200, 1599)},
		{name: "not a PNG", portrait: jpegBuf.Bytes()},
		{name: "empty file", portrait: []byte{}},
		{name: "truncated", portrait: portraitPNG[:len(portraitPNG)/2]},
		{name: "corrupt data", portrait: corrupt},
		{name: "text metadata chunk", portrait: withChunkBeforeIDAT(t, portraitPNG, pngChunk("tEXt", []byte("Comment\x00hi")))},
		{name: "physical size chunk", portrait: withChunkBeforeIDAT(t, portraitPNG, pngChunk("pHYs", make([]byte, 9)))},
		{name: "embedded profile chunk", portrait: withChunkBeforeIDAT(t, portraitPNG, pngChunk("iCCP", []byte("p\x00\x00x")))},
		{name: "trailing bytes after IEND", portrait: append(bytes.Clone(portraitPNG), 0x00)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) {
				if tt.portrait != nil {
					f.portrait = tt.portrait
				}

				if tt.display != nil {
					f.display = tt.display
				}
			})

			if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrInvalidImage) {
				t.Fatalf("Load() error = %v, want ErrInvalidImage", err)
			}
		})
	}
}

func TestLoadRejectsSymlinkedAsset(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, nil)
	fsys["species/1001-carolina-wren/display.png"] = &fstest.MapFile{Data: []byte("elsewhere"), Mode: fs.ModeSymlink}

	if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrNotRegularFile) {
		t.Fatalf("Load() error = %v, want ErrNotRegularFile", err)
	}
}

func TestLoadRejectsOversizedAsset(t *testing.T) {
	t.Parallel()

	// The documented per-file limit is 32 MiB.
	const limit = 32 << 20

	huge := bytes.Repeat([]byte{0}, limit+1)
	fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) { f.portrait = huge })

	if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrInvalidImage) {
		t.Fatalf("Load() error = %v, want ErrInvalidImage", err)
	}
}
