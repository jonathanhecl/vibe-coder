package tui

import (
	"fmt"
	"io"
	"strings"
)

// Row is one configuration line: a label with its value in both the plain and
// the styled variant. The startup banner and the /status table share it, so both
// surfaces stay word-for-word identical.
//
// A row that carries a Section is grouped: the header is printed once, when the
// section starts, and its rows follow as indented bullets. Rows without a
// Section form the main list.
type Row struct {
	// Label names the row in both layouts.
	Label string
	// Short replaces Label in the aligned layout when a long name would widen
	// the whole column.
	Short string
	// PlainLabel replaces Label in the unstyled layout, for rows whose line
	// reads differently outside the table (such as "Session started").
	PlainLabel string
	// Plain is the unstyled value; Render draws the colored one.
	Plain  string
	Render func(Style) string
	// Section is the header the row is listed under.
	Section string
}

// RoleSection groups the model rows in the startup banner and in /status.
const RoleSection = "Models"

// plainLabel returns the name used in the unstyled layout.
func (r Row) plainLabel() string {
	if label := strings.TrimSpace(r.PlainLabel); label != "" {
		return label
	}
	return r.Label
}

// short returns the name used in the aligned layout.
func (r Row) short() string {
	if label := strings.TrimSpace(r.Short); label != "" {
		return label
	}
	return r.Label
}

// WriteRows renders rows to w: one "Label: value" line each when styling is
// off, and an aligned, colored layout when it is on. Both layouts pad every row
// of a group to the widest name in it, so a column stays readable.
func WriteRows(w io.Writer, st Style, rows []Row) {
	if !st.Enabled() {
		writePlainRows(w, rows)
		return
	}
	writeStyledRows(w, st, rows)
}

func writePlainRows(w io.Writer, rows []Row) {
	section := ""
	for _, r := range rows {
		if r.Section == "" {
			section = ""
			fmt.Fprintf(w, "%s: %s\n", r.plainLabel(), r.Plain)
			continue
		}
		if r.Section != section {
			section = r.Section
			fmt.Fprintln(w, section)
		}
		fmt.Fprintf(w, "  - %s: %s\n", r.plainLabel(), r.Plain)
	}
}

func writeStyledRows(w io.Writer, st Style, rows []Row) {
	flatWidth, bulletWidth := 0, 0
	for _, r := range rows {
		if r.Section != "" {
			bulletWidth = max(bulletWidth, len(r.short()))
			continue
		}
		flatWidth = max(flatWidth, len(r.short()))
	}

	section := ""
	for _, r := range rows {
		value := st.BrightWhite(r.Plain)
		if r.Render != nil {
			value = r.Render(st)
		}
		if r.Section == "" {
			section = ""
			fmt.Fprintf(w, "  %s  %s\n", st.BoldCyan(pad(r.short(), flatWidth)), value)
			continue
		}
		if r.Section != section {
			section = r.Section
			fmt.Fprintf(w, "  %s\n", st.BoldCyan(section))
		}
		fmt.Fprintf(w, "    %s%s  %s\n", st.Gray("- "), st.Cyan(pad(r.short(), bulletWidth)), value)
	}
}

// pad right-pads a label so the values of a group line up.
func pad(label string, width int) string {
	return fmt.Sprintf("%-*s", width, label)
}
