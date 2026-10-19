// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"fmt"
	"net/url"
	"strconv"

	"imvault/internal/store"
)

type libraryDates struct{ Sort, Year, Month string }

func applyLibraryDates(query url.Values, filter *store.FileQuery) (libraryDates, error) {
	dates := libraryDates{Sort: query.Get("sort"), Year: query.Get("year"), Month: query.Get("month")}
	if dates.Sort != "" && dates.Sort != "uploaded" && dates.Sort != "taken" {
		return dates, fmt.Errorf("sort must be uploaded or taken")
	}
	filter.SortTaken = dates.Sort == "taken"
	if dates.Year == "unknown" {
		if dates.Month != "" {
			return dates, fmt.Errorf("a month needs a known year")
		}
		filter.UnknownTaken = true
		return dates, nil
	}
	if dates.Year != "" {
		if !validLibraryNumber(dates.Year, 4, 9999) {
			return dates, fmt.Errorf("year must have four digits")
		}
		filter.TakenPrefix = dates.Year + "-"
	}
	if dates.Month != "" {
		if dates.Year == "" || !validLibraryNumber(dates.Month, 2, 12) {
			return dates, fmt.Errorf("choose a year and a month from 01 to 12")
		}
		filter.TakenPrefix += dates.Month + "-"
	}
	return dates, nil
}

func validLibraryNumber(raw string, width, maximum int) bool {
	value, err := strconv.Atoi(raw)
	return err == nil && len(raw) == width && value >= 1 && value <= maximum && fmt.Sprintf("%0*d", width, value) == raw
}
