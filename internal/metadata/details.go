// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/evanoberholster/imagemeta"
	"github.com/evanoberholster/imagemeta/meta/exif"
)

// Details are the descriptive fields read out of a file's metadata.
//
// They are read once, at upload, and stored. Nothing re-reads an original to
// display them, and that is the point: a file whose metadata is no longer
// served can still describe itself to the people allowed to see it. The bytes
// and the page are separate channels, and the date a photograph was taken is
// worth keeping even where the coordinates are not.
//
// Everything is a rendered string. Formatting belongs with the reading rather
// than with the template, and a stored string cannot be misformatted twice.
type Details struct {
	Taken    string `json:"taken,omitempty"`
	Camera   string `json:"camera,omitempty"`
	Lens     string `json:"lens,omitempty"`
	Exposure string `json:"exposure,omitempty"`
	Aperture string `json:"aperture,omitempty"`
	ISO      string `json:"iso,omitempty"`
	Focal    string `json:"focal,omitempty"`
	Software string `json:"software,omitempty"`

	// The identifying fields: where a picture was taken, and who by.
	Artist    string   `json:"artist,omitempty"`
	Copyright string   `json:"copyright,omitempty"`
	Serial    string   `json:"serial,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	Altitude  *float64 `json:"altitude,omitempty"`
}

// Field is one rendered line of the details.
type Field struct {
	Label string
	Value string
	// Identifying marks the fields that are withheld when a file is not
	// showing its metadata.
	Identifying bool
}

// Fields renders the details in the order a person would read them: what it
// was taken with and when, then how, and then where it is marked as such.
func (d *Details) Fields() []Field {
	if d == nil {
		return nil
	}

	all := []Field{
		{Label: "Taken", Value: d.Taken},
		{Label: "Camera", Value: d.Camera},
		{Label: "Lens", Value: d.Lens},
		{Label: "Exposure", Value: d.Exposure},
		{Label: "Aperture", Value: d.Aperture},
		{Label: "ISO", Value: strconvOrEmpty(d.ISO)},
		{Label: "Focal length", Value: d.Focal},
		{Label: "Software", Value: d.Software},
		{Label: "Artist", Value: d.Artist, Identifying: true},
		{Label: "Copyright", Value: d.Copyright, Identifying: true},
		{Label: "Serial number", Value: d.Serial, Identifying: true},
		{Label: "Location", Value: d.Location(), Identifying: true},
	}

	out := make([]Field, 0, len(all))
	for _, field := range all {
		if field.Value != "" {
			out = append(out, field)
		}
	}
	return out
}

// Location renders the coordinates, or empty when there are none.
//
// Decimal degrees rather than degrees and minutes: it is what a person can
// paste somewhere, and there is no honest way to show a location without
// showing it.
func (d *Details) Location() string {
	if d == nil || d.Latitude == nil || d.Longitude == nil {
		return ""
	}

	out := fmt.Sprintf("%.5f, %.5f", *d.Latitude, *d.Longitude)
	if d.Altitude != nil && *d.Altitude != 0 {
		out += fmt.Sprintf(" (%.0fm)", *d.Altitude)
	}
	return out
}

// HasIdentifying reports whether anything withheld is actually present, so the
// page can say what is being kept back rather than saying nothing.
func (d *Details) HasIdentifying() bool {
	if d == nil {
		return false
	}
	return d.Location() != "" || d.Artist != "" || d.Copyright != "" || d.Serial != ""
}

// Empty reports whether there is nothing worth showing.
func (d *Details) Empty() bool { return d == nil || len(d.Fields()) == 0 }

func strconvOrEmpty(value string) string { return strings.TrimSpace(value) }

// Encode renders the details for storage. A nil or empty set encodes to an
// empty string rather than to "{}", so that "this file has no details" has one
// representation in the database.
func (d *Details) Encode() string {
	if d.Empty() {
		return ""
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(raw)
}

// DecodeDetails reads stored details. Anything unreadable is treated as none:
// this is display data, and a file that cannot describe itself is not a file
// anybody needs an error for.
func DecodeDetails(raw string) *Details {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var details Details
	if err := json.Unmarshal([]byte(raw), &details); err != nil {
		return nil
	}
	if details.Empty() {
		return nil
	}
	return &details
}

// Extract reads the descriptive fields out of a file.
//
// A file with no metadata, or one this cannot read, is not an error to worry
// about: the upload is about the picture, and most of what this accepts carries
// nothing at all. It still reports one, so a caller can log it at debug.
//
// The reader is left wherever the parser finished, which is the caller's
// problem to rewind.
func Extract(r io.ReadSeeker) (*Details, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	exif, err := decode(r)
	if err != nil {
		return nil, err
	}

	details := &Details{
		Camera:   cameraName(exif.CameraMakeID.String(), exif.IFD0.Model),
		Lens:     strings.TrimSpace(exif.ExifIFD.LensModel),
		Software: strings.TrimSpace(exif.IFD0.Software),

		Artist:    strings.TrimSpace(exif.IFD0.Artist),
		Copyright: strings.TrimSpace(exif.IFD0.Copyright),
		Serial:    strings.TrimSpace(exif.CameraSerial),
	}

	// Each of these marshals a zero value to "0.00" rather than to nothing, so
	// a file with no metadata would otherwise come out looking like one taken
	// at f/0 for a hundredth of a second.
	if value := float64(exif.ExifIFD.ExposureTime); value != 0 {
		details.Exposure = textOf(exif.ExifIFD.ExposureTime)
	}
	if value := float64(exif.ExifIFD.FNumber); value != 0 {
		details.Aperture = textOf(exif.ExifIFD.FNumber)
	}
	if value := float64(exif.ExifIFD.FocalLength); value != 0 {
		details.Focal = textOf(exif.ExifIFD.FocalLength)
	}
	if exif.ExifIFD.ISOSpeedRatings > 0 {
		details.ISO = strconv.FormatUint(uint64(exif.ExifIFD.ISOSpeedRatings), 10)
	}

	// The original time is what a photograph was taken at; CreateDate is the
	// fallback for cameras that only wrote that one.
	when := exif.ExifIFD.DateTimeOriginal
	if when.IsZero() {
		when = exif.ExifIFD.CreateDate
	}
	if !when.IsZero() {
		details.Taken = when.Format("2 January 2006 at 15:04")
	}

	// A location of exactly zero for both is the null island, which is what a
	// camera writes when it had no fix rather than one off the coast of Africa.
	if lat, lon := exif.GPS.Latitude(), exif.GPS.Longitude(); lat != 0 || lon != 0 {
		details.Latitude, details.Longitude = &lat, &lon
		if altitude := float64(exif.GPS.Altitude()); altitude != 0 {
			details.Altitude = &altitude
		}
	}
	if gps, handled := jpegLocation(r); handled {
		details.Latitude, details.Longitude, details.Altitude = gps.latitude, gps.longitude, gps.altitude
	}

	if details.Empty() {
		return nil, nil
	}
	return details, nil
}

// decode wraps the parser so that a malformed file cannot take the process down
// with it. This runs on everything anybody uploads, which is the definition of
// untrusted input, and a third-party parser is the last place to be optimistic.
func decode(r io.ReadSeeker) (parsed exif.Exif, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("metadata: the parser gave up on this file: %v", recovered)
		}
	}()
	return imagemeta.Decode(r)
}

// unknownMake is what the library calls a make it does not recognise.
const unknownMake = "Unknown"

// cameraName joins a make and a model without repeating the make, since most
// cameras put it at the front of the model too.
//
// The make passed in is the library's identified one rather than IFD0's raw
// value, and that matters: identifying a make normalises the bytes *in place* —
// lowercased, with spaces and punctuation stripped, and with the tail of the
// original left behind — so IFD0.Make holds something mangled for every camera
// the library recognises. The identified name is the clean one, and an
// unrecognised camera is better described by its model alone than by its own
// name spelled wrongly.
func cameraName(make, model string) string {
	make, model = strings.TrimSpace(make), strings.TrimSpace(model)
	if make == unknownMake {
		make = ""
	}

	switch {
	case make == "":
		return model
	case model == "":
		return make
	case strings.HasPrefix(strings.ToLower(model), strings.ToLower(make)):
		return model
	}
	return make + " " + model
}

// textOf renders one of the library's numeric types through its own text
// marshaler, which already knows that an exposure is "1/250" and a focal
// length is "50mm".
func textOf(value interface{ MarshalText() ([]byte, error) }) string {
	if value == nil {
		return ""
	}
	out, err := value.MarshalText()
	if err != nil {
		return ""
	}
	return string(out)
}
