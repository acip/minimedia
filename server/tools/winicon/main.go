// Command winicon draws the MiniMedia icon (a white play triangle on a red
// rounded square, like the TV app's logo) and writes it as a Windows
// resource object that `go build` links into minimedia.exe automatically:
//
//	cd server && go run ./tools/winicon
//
// It writes cmd/minimedia/rsrc_windows_amd64.syso (the icon in every size
// Windows asks for) and cmd/minimedia/minimedia.ico. Only the standard
// library and golang.org/x/image are used.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/image/vector"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256}

var (
	red   = color.NRGBA{0xE5, 0x09, 0x14, 0xFF}
	white = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// render draws the icon at size×size pixels.
func render(size int) *image.NRGBA {
	s := float32(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Rounded square with a small margin, so it doesn't look oversized next
	// to other icons.
	m, r := s*0.06, s*0.2
	x0, y0, x1, y1 := m, m, s-m, s-m
	sq := vector.NewRasterizer(size, size)
	sq.MoveTo(x0+r, y0)
	sq.LineTo(x1-r, y0)
	sq.QuadTo(x1, y0, x1, y0+r)
	sq.LineTo(x1, y1-r)
	sq.QuadTo(x1, y1, x1-r, y1)
	sq.LineTo(x0+r, y1)
	sq.QuadTo(x0, y1, x0, y1-r)
	sq.LineTo(x0, y0+r)
	sq.QuadTo(x0, y0, x0+r, y0)
	sq.ClosePath()
	sq.Draw(img, img.Bounds(), image.NewUniform(red), image.Point{})

	// Play triangle, optically centred (nudged right of its bounding box).
	tri := vector.NewRasterizer(size, size)
	tri.MoveTo(s*0.38, s*0.28)
	tri.LineTo(s*0.75, s*0.50)
	tri.LineTo(s*0.38, s*0.72)
	tri.ClosePath()
	tri.Draw(img, img.Bounds(), image.NewUniform(white), image.Point{})
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

// ico packs PNG images into a .ico file (PNG entries work on Windows Vista+).
func ico(pngs [][]byte) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, [3]uint16{0, 1, uint16(len(pngs))})
	off := 6 + 16*len(pngs)
	for i, p := range pngs {
		w := byte(sizes[i] % 256) // 0 means 256
		b.Write([]byte{w, w, 0, 0})
		binary.Write(&b, le, [2]uint16{1, 32})
		binary.Write(&b, le, [2]uint32{uint32(len(p)), uint32(off)})
		off += len(p)
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

// groupIcon is the RT_GROUP_ICON resource: like an .ico header, but each
// entry names an RT_ICON resource id instead of a file offset.
func groupIcon(pngs [][]byte) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, [3]uint16{0, 1, uint16(len(pngs))})
	for i, p := range pngs {
		w := byte(sizes[i] % 256)
		b.Write([]byte{w, w, 0, 0})
		binary.Write(&b, le, [2]uint16{1, 32})
		binary.Write(&b, le, uint32(len(p)))
		binary.Write(&b, le, uint16(i+1))
	}
	return b.Bytes()
}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langNeutral = 0
)

type resource struct {
	typ, id uint32
	data    []byte
}

// coff builds a COFF object holding one .rsrc section with the resources,
// the format the Go linker merges into a Windows executable. The resource
// directory's data entries hold section-relative offsets, relocated to image
// addresses with IMAGE_REL_AMD64_ADDR32NB.
func coff(res []resource) []byte {
	// Group by type, keeping the ascending order the directory requires
	// (the caller passes them sorted by type, then id).
	type typeGroup struct {
		typ   uint32
		items []resource
	}
	var groups []typeGroup
	for _, r := range res {
		if len(groups) == 0 || groups[len(groups)-1].typ != r.typ {
			groups = append(groups, typeGroup{typ: r.typ})
		}
		g := &groups[len(groups)-1]
		g.items = append(g.items, r)
	}

	dirSize := func(entries int) int { return 16 + 8*entries }
	// Layout: root dir, type dirs, language dirs, data entries, then data.
	off := dirSize(len(groups))
	typeDirOff := make([]int, len(groups))
	for i, g := range groups {
		typeDirOff[i] = off
		off += dirSize(len(g.items))
	}
	langDirOff := map[[2]uint32]int{}
	for _, g := range groups {
		for _, it := range g.items {
			langDirOff[[2]uint32{it.typ, it.id}] = off
			off += dirSize(1)
		}
	}
	entryOff := map[[2]uint32]int{}
	for _, g := range groups {
		for _, it := range g.items {
			entryOff[[2]uint32{it.typ, it.id}] = off
			off += 16
		}
	}
	dataOff := map[[2]uint32]int{}
	for _, g := range groups {
		for _, it := range g.items {
			off = (off + 7) &^ 7
			dataOff[[2]uint32{it.typ, it.id}] = off
			off += len(it.data)
		}
	}
	sect := make([]byte, (off+7)&^7)
	le := binary.LittleEndian

	writeDir := func(at int, ids []uint32, targets []int, subdir bool) {
		le.PutUint16(sect[at+14:], uint16(len(ids))) // NumberOfIdEntries
		for i, id := range ids {
			e := at + 16 + 8*i
			le.PutUint32(sect[e:], id)
			t := uint32(targets[i])
			if subdir {
				t |= 0x80000000
			}
			le.PutUint32(sect[e+4:], t)
		}
	}
	var rootIDs []uint32
	var rootTargets []int
	for i, g := range groups {
		rootIDs = append(rootIDs, g.typ)
		rootTargets = append(rootTargets, typeDirOff[i])
	}
	writeDir(0, rootIDs, rootTargets, true)

	var relocs []uint32 // offsets of OffsetToData fields
	for i, g := range groups {
		var ids []uint32
		var targets []int
		for _, it := range g.items {
			k := [2]uint32{it.typ, it.id}
			ids = append(ids, it.id)
			targets = append(targets, langDirOff[k])
			writeDir(langDirOff[k], []uint32{langNeutral}, []int{entryOff[k]}, false)
			e := entryOff[k]
			le.PutUint32(sect[e:], uint32(dataOff[k])) // addend; relocated to an RVA
			le.PutUint32(sect[e+4:], uint32(len(it.data)))
			relocs = append(relocs, uint32(e))
			copy(sect[dataOff[k]:], it.data)
		}
		writeDir(typeDirOff[i], ids, targets, true)
	}

	const fileHeader, sectionHeader, relocSize, symSize = 20, 40, 10, 18
	rawOff := fileHeader + sectionHeader
	relocOff := rawOff + len(sect)
	symOff := relocOff + relocSize*len(relocs)

	var b bytes.Buffer
	binary.Write(&b, le, uint16(0x8664)) // IMAGE_FILE_MACHINE_AMD64
	binary.Write(&b, le, uint16(1))      // sections
	binary.Write(&b, le, uint32(0))      // timestamp
	binary.Write(&b, le, uint32(symOff))
	binary.Write(&b, le, uint32(1)) // symbols
	binary.Write(&b, le, uint16(0)) // optional header size
	binary.Write(&b, le, uint16(0)) // characteristics

	b.WriteString(".rsrc\x00\x00\x00")
	binary.Write(&b, le, [2]uint32{0, 0}) // virtual size, address
	binary.Write(&b, le, uint32(len(sect)))
	binary.Write(&b, le, uint32(rawOff))
	binary.Write(&b, le, uint32(relocOff))
	binary.Write(&b, le, uint32(0)) // line numbers
	binary.Write(&b, le, uint16(len(relocs)))
	binary.Write(&b, le, uint16(0))
	binary.Write(&b, le, uint32(0x40000040)) // initialized data, readable

	b.Write(sect)
	for _, r := range relocs {
		binary.Write(&b, le, r)
		binary.Write(&b, le, uint32(0)) // symbol 0: the section
		binary.Write(&b, le, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	b.WriteString(".rsrc\x00\x00\x00")
	binary.Write(&b, le, uint32(0)) // value
	binary.Write(&b, le, int16(1))  // section number
	binary.Write(&b, le, uint16(0)) // type
	b.WriteByte(3)                  // IMAGE_SYM_CLASS_STATIC
	b.WriteByte(0)                  // aux symbols
	binary.Write(&b, le, uint32(4)) // empty string table
	return b.Bytes()
}

func main() {
	out := filepath.Join("cmd", "minimedia")
	if _, err := os.Stat(out); err != nil {
		log.Fatal("run this from the server folder: go run ./tools/winicon")
	}
	var pngs [][]byte
	for _, s := range sizes {
		img := render(s)
		// Flatten onto transparent explicitly so every size is 32-bit RGBA.
		dst := image.NewNRGBA(img.Bounds())
		draw.Draw(dst, dst.Bounds(), img, image.Point{}, draw.Src)
		pngs = append(pngs, encodePNG(dst))
	}
	var res []resource
	for i, p := range pngs {
		res = append(res, resource{typ: rtIcon, id: uint32(i + 1), data: p})
	}
	res = append(res, resource{typ: rtGroupIcon, id: 1, data: groupIcon(pngs)})

	if err := os.WriteFile(filepath.Join(out, "rsrc_windows_amd64.syso"), coff(res), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "minimedia.ico"), ico(pngs), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s/rsrc_windows_amd64.syso and minimedia.ico (%d sizes)", out, len(sizes))
}
