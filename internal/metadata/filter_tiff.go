// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import "encoding/binary"

// Only fields with self-contained values are copied. Pointers, maker notes,
// thumbnails and private tags cannot be relocated safely or classified as
// location-free. Keep the camera, date, exposure, lens and attribution fields.
var cameraTags = map[uint16]bool{
	0x010f: true, 0x0110: true, 0x0112: true, 0x0131: true, 0x0132: true, 0x013b: true, 0x8298: true,
}
var exposureTags = map[uint16]bool{
	0x829a: true, 0x829d: true, 0x8822: true, 0x8827: true, 0x8830: true, 0x8831: true, 0x8832: true, 0x8833: true,
	0x9000: true, 0x9003: true, 0x9004: true, 0x9010: true, 0x9011: true, 0x9012: true,
	0x9101: true, 0x9102: true, 0x9201: true, 0x9202: true, 0x9203: true, 0x9204: true, 0x9205: true,
	0x9207: true, 0x9208: true, 0x9209: true, 0x920a: true, 0x9290: true, 0x9291: true, 0x9292: true,
	0xa000: true, 0xa001: true, 0xa002: true, 0xa003: true, 0xa20e: true, 0xa20f: true, 0xa210: true,
	0xa217: true, 0xa300: true, 0xa301: true, 0xa401: true, 0xa402: true, 0xa403: true, 0xa404: true,
	0xa405: true, 0xa406: true, 0xa407: true, 0xa408: true, 0xa409: true, 0xa40a: true,
	0xa430: true, 0xa431: true, 0xa432: true, 0xa433: true, 0xa434: true, 0xa435: true,
}

type filteredEntry struct {
	tag, kind uint16
	count     uint32
	value     []byte
}

func filteredTIFF(data []byte, camera bool) []byte {
	if len(data) < 8 {
		return nil
	}
	var order binary.ByteOrder
	switch string(data[:4]) {
	case "II\x2a\x00":
		order = binary.LittleEndian
	case "MM\x00\x2a":
		order = binary.BigEndian
	default:
		return nil
	}
	tiff := gpsTIFF{data: data, order: order}
	root := tiff.directory(order.Uint32(data[4:]))
	var first, child []filteredEntry
	pointerTag := uint16(0x8825)
	if camera {
		first = tiff.selectedEntries(root, func(tag uint16) bool { return cameraTags[tag] })
		pointerTag = 0x8769
	}
	if pointer := tiff.entry(root, pointerTag, 4, 1); pointer != nil {
		directory := tiff.directory(order.Uint32(pointer))
		child = tiff.selectedEntries(directory, func(tag uint16) bool {
			if camera {
				return exposureTags[tag]
			}
			return tag <= 0x001f
		})
	}
	if len(first)+len(child) == 0 {
		return nil
	}
	return encodeFilteredTIFF(order, first, child, pointerTag)
}

func (t gpsTIFF) selectedEntries(directory []byte, allowed func(uint16) bool) []filteredEntry {
	var entries []filteredEntry
	seen := map[uint16]bool{}
	for len(directory) >= 12 {
		e := directory[:12]
		directory = directory[12:]
		tag, kind, count := t.order.Uint16(e), t.order.Uint16(e[2:]), t.order.Uint32(e[4:])
		if !allowed(tag) || seen[tag] {
			continue
		}
		value := t.entryData(e, kind, count)
		if value == nil {
			continue
		}
		entries = append(entries, filteredEntry{tag, kind, count, value})
		seen[tag] = true
	}
	return entries
}

func (t gpsTIFF) entryData(e []byte, kind uint16, count uint32) []byte {
	// TIFF primitive types 1–12. IFD pointers (13) and other unknown types
	// are excluded rather than copied with stale offsets.
	sizes := [...]uint64{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8}
	if kind == 0 || int(kind) >= len(sizes) {
		return nil
	}
	size := sizes[kind] * uint64(count)
	if size == 0 || size > 4096 {
		return nil
	}
	if size <= 4 {
		return e[8 : 8+size]
	}
	at := uint64(t.order.Uint32(e[8:]))
	if at < 8 || at+size > uint64(len(t.data)) {
		return nil
	}
	return t.data[at : at+size]
}

func encodeFilteredTIFF(order binary.ByteOrder, root, child []filteredEntry, pointerTag uint16) []byte {
	if len(child) != 0 {
		offset := make([]byte, 4)
		order.PutUint32(offset, uint32(8+2+(len(root)+1)*12+4))
		root = append(root, filteredEntry{pointerTag, 4, 1, offset})
	}
	end := 8 + 2 + len(root)*12 + 4
	if len(child) != 0 {
		end += 2 + len(child)*12 + 4
	}
	out := make([]byte, end)
	copy(out, "II\x2a\x00")
	if order == binary.BigEndian {
		copy(out, "MM\x00\x2a")
	}
	order.PutUint32(out[4:], 8)
	at := 8
	for _, entries := range [][]filteredEntry{root, child} {
		if len(entries) == 0 {
			continue
		}
		order.PutUint16(out[at:], uint16(len(entries)))
		at += 2
		for _, e := range entries {
			order.PutUint16(out[at:], e.tag)
			order.PutUint16(out[at+2:], e.kind)
			order.PutUint32(out[at+4:], e.count)
			if len(e.value) <= 4 {
				copy(out[at+8:at+12], e.value)
			} else {
				order.PutUint32(out[at+8:], uint32(len(out)))
				out = append(out, e.value...)
				if len(out)&1 != 0 {
					out = append(out, 0)
				}
			}
			at += 12
		}
		at += 4
	}
	if len(out) > 65527 {
		return nil
	}
	return out
}
