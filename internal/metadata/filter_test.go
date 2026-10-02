// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"

	"imvault/internal/exifwrite"
)

func TestFilterKeepsEXIFAndGPSIndependently(t *testing.T) {
	jpeg := jpegWithExif(t)
	block := imageTIFFs(jpeg)[0]
	pngData := insertPNGChunk(t, samplePNG(t), "eXIf", block)
	pngData = insertPNGChunk(t, pngData, "iTXt", []byte("hidden-copy-of-GPS"))
	webp := append(sampleWebP(t, false), webpChunk("EXIF", block)...)
	webp = append(webp, webpChunk("XMP ", []byte("hidden-copy-of-GPS"))...)
	binary.LittleEndian.PutUint32(webp[4:], uint32(len(webp)-8))
	jpeg = insertAfterSOI(t, jpeg, segment(0xED, []byte("hidden-copy-of-GPS")))
	for name, data := range map[string][]byte{"jpeg": jpeg, "png": pngData, "webp": webp} {
		for _, camera := range []bool{true, false} {
			t.Run(name+map[bool]string{true: "-camera", false: "-location"}[camera], func(t *testing.T) {
				out, ok := Filter(data, "image/"+name, camera, !camera)
				if !ok {
					t.Fatal("filter refused fixture")
				}
				if bytes.Contains(out, []byte("hidden-copy-of-GPS")) {
					t.Fatal("secondary metadata copy survived")
				}
				blocks := imageTIFFs(out)
				if len(blocks) != 1 {
					t.Fatalf("got %d EXIF blocks", len(blocks))
				}
				gps := tiffLocation(blocks[0])
				if camera == (gps.latitude != nil) {
					t.Fatalf("GPS presence disagrees with choice: %+v", gps)
				}
				if camera != bytes.Contains(blocks[0], []byte("TestCam One")) {
					t.Fatal("camera presence disagrees with choice")
				}
				if camera != bytes.Contains(blocks[0], []byte("A Photographer")) {
					t.Fatal("artist presence disagrees with choice")
				}
				if !camera && (*gps.latitude < 51.50 || *gps.longitude > -0.12) {
					t.Fatal("GPS values changed")
				}
				cleanBefore, _ := Strip(data, "")
				cleanAfter, _ := Strip(out, "")
				if !bytes.Equal(cleanBefore, cleanAfter) {
					t.Fatal("compressed image bytes changed")
				}
				again, ok := Filter(out, "", camera, !camera)
				if !ok || !bytes.Equal(out, again) {
					t.Fatal("filter is not idempotent")
				}
				if name == "jpeg" {
					decodeJPEG(t, out)
				}
				if name == "png" {
					if _, err := png.Decode(bytes.NewReader(out)); err != nil {
						t.Fatal(err)
					}
				}
				if name == "webp" && webpFlagsOf(t, out)&webpFlagExif == 0 {
					t.Fatal("EXIF flag missing")
				}
			})
		}
	}
}

func TestFilteredGPSByteOrdersAndBackwardsValues(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, backwards := range []bool{true, false} {
			out := filteredTIFF(gpsBlock(order, backwards), false)
			gps := tiffLocation(out)
			if gps.latitude == nil || *gps.latitude != -33.5 || *gps.longitude != -18.25 || *gps.altitude != -15 {
				t.Fatal("filtered GPS changed")
			}
			if camera := filteredTIFF(gpsBlock(order, backwards), true); len(camera) != 0 {
				t.Fatal("GPS-only input leaked through camera filter")
			}
		}
	}
}

func TestFilterDropsOpaqueAndMalformedExif(t *testing.T) {
	block := exifwrite.Block(exifwrite.Tags{Model: "Camera", Latitude: 1, Longitude: 2})[6:]
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0xffffffff) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[8:], 0xffff) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[18:], 0xffffffff) },
	} {
		bad := append([]byte{}, block...)
		mutate(bad)
		data := insertAfterSOI(t, sampleJPEG(t), segment(jpegAPP1, append([]byte("Exif\x00\x00"), bad...)))
		out, ok := Filter(data, "", true, false)
		if !ok {
			t.Fatal("metadata corruption should not destroy a valid image")
		}
		for _, b := range imageTIFFs(out) {
			if tiffLocation(b).latitude != nil {
				t.Fatal("bad EXIF disclosed GPS")
			}
		}
	}
	if _, ok := Filter([]byte("unhandled"), "image/jpeg", true, false); ok {
		t.Fatal("unhandled input accepted")
	}
}

func FuzzFilteredTIFF(f *testing.F) {
	f.Add(gpsBlock(binary.LittleEndian, false))
	f.Add(gpsBlock(binary.BigEndian, true))
	f.Add(exifwrite.Block(exifwrite.Tags{Model: "Camera", Latitude: 1, Longitude: 2})[6:])
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, camera := range []bool{true, false} {
			out := filteredTIFF(data, camera)
			if len(out) > 65527 {
				t.Fatal("unbounded EXIF")
			}
			if camera && tiffLocation(out).latitude != nil {
				t.Fatal("camera copy contains GPS")
			}
			if len(out) > 0 && !bytes.Equal(out, filteredTIFF(out, camera)) {
				t.Fatal("filter is not idempotent")
			}
		}
		Filter(data, "", true, false)
		Filter(data, "", false, true)
	})
}
