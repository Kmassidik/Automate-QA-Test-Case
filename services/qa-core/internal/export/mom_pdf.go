package export

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"qa-core/internal/contract"
)

// MOMPDF renders a Minutes-of-Meeting to a PDF that mirrors the frozen docx
// template: a centred "MINUTES OF MEETING" title, a bordered header block
// (Date/Time · Location · Purpose · Attendees), a numbered "Meeting Discussions
// and Info" table, a numbered "Follow Up" table, and a prepared/approved footer
// with the 24-hour remarks line. Variable text comes from the model; the fixed
// strings live here.
func MOMPDF(m contract.MOM) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(false, 15) // manual pagination keeps borders in sync
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("") // map UTF-8 punctuation to cp1252

	const (
		contentW = 180.0 // A4 210mm - 2*15mm margins
		lineH    = 5.0
		bottom   = 282.0 // page height 297 - 15mm margin
	)
	left := 15.0

	// Title.
	pdf.SetFont("Arial", "B", 20)
	pdf.CellFormat(contentW, 12, tr("MINUTES OF MEETING"), "", 1, "C", false, 0, "")
	pdf.Ln(3)

	gray := func() { pdf.SetFillColor(217, 217, 217) }
	white := func() { pdf.SetFillColor(255, 255, 255) }

	// ---- Header block ------------------------------------------------------
	// Date/Time row: label | value | label | value (four columns).
	labelW := 33.0
	dtValW := 64.0
	locLabelW := 25.0
	locValW := contentW - labelW - dtValW - locLabelW
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(labelW, 8, tr("Date / Time"), "1", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(dtValW, 8, tr(orDash(m.DateTime)), "1", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(locLabelW, 8, tr("Location:"), "1", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(locValW, 8, tr(orDash(m.Location)), "1", 1, "L", false, 0, "")

	// Purpose / Attendees rows use a wider label column so "Purpose of Meeting:"
	// doesn't run into its value.
	wideLabelW := 44.0
	labelRow(pdf, tr, left, wideLabelW, contentW-wideLabelW, "Purpose of Meeting:", orDash(m.Purpose), lineH)
	labelRow(pdf, tr, left, wideLabelW, contentW-wideLabelW, "Attendees:", strings.Join(m.Attendees, "\n"), lineH)
	pdf.Ln(4)

	// ---- Meeting Discussions and Info -------------------------------------
	sectionBar(pdf, tr, contentW, "Meeting Discussions and Info", gray, white)
	tableHeader(pdf, tr, contentW, gray, white)
	y := pdf.GetY()
	for i, it := range m.Discussions {
		y = numberedRow(pdf, tr, left, contentW, y, lineH, bottom, i+1, it.Title, it.Description, gray, white)
	}
	pdf.SetY(y)
	pdf.Ln(4)

	// ---- Follow Up --------------------------------------------------------
	if needPage(pdf, 24, bottom) {
		pdf.AddPage()
	}
	sectionBar(pdf, tr, contentW, "Follow Up", gray, white)
	tableHeader(pdf, tr, contentW, gray, white)
	y = pdf.GetY()
	for i, it := range m.FollowUps {
		y = numberedRow(pdf, tr, left, contentW, y, lineH, bottom, i+1, it.Title, it.Description, gray, white)
	}
	pdf.SetY(y)
	pdf.Ln(6)

	// ---- Footer -----------------------------------------------------------
	if needPage(pdf, 40, bottom) {
		pdf.AddPage()
	}
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(contentW, 7, tr("Prepared by:"), "LTR", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(115, 7, tr("Name: "+m.PreparedBy), "LB", 0, "L", false, 0, "")
	pdf.CellFormat(contentW-115, 7, tr("Date: "+m.DateTime), "RB", 1, "L", false, 0, "")
	pdf.Ln(3)
	pdf.SetFont("Arial", "I", 9)
	pdf.MultiCell(contentW, 4.5, tr("*Remarks: If there is no amendment received within 24 hours (on weekdays) after this MoM is sent, it will be considered as the final version."), "", "L", false)
	pdf.Ln(2)
	pdf.SetFont("Arial", "", 10)
	half := contentW / 2
	pdf.CellFormat(half, 20, tr("Minutes Prepared by:"), "1", 0, "LT", false, 0, "")
	pdf.CellFormat(half, 20, tr("Minutes Approved by:"), "1", 1, "LT", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// labelRow draws a "label | value" row where the value may wrap to several lines;
// both cells share the measured height and keep their borders aligned.
func labelRow(pdf *fpdf.Fpdf, tr func(string) string, left, labelW, valW float64, label, value string, lineH float64) {
	value = orDash(value)
	pdf.SetFont("Arial", "", 10)
	lines := pdf.SplitText(tr(value), valW-2)
	h := float64(len(lines)) * lineH
	if h < 8 {
		h = 8
	}
	x0, y0 := left, pdf.GetY()
	pdf.Rect(x0, y0, labelW, h, "D")
	pdf.SetFont("Arial", "B", 10)
	pdf.SetXY(x0+1, y0+1)
	pdf.CellFormat(labelW-2, lineH, tr(label), "", 0, "L", false, 0, "")
	pdf.Rect(x0+labelW, y0, valW, h, "D")
	pdf.SetXY(x0+labelW+1, y0+1)
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(valW-2, lineH, tr(value), "", "L", false)
	pdf.SetXY(x0, y0+h)
}

func sectionBar(pdf *fpdf.Fpdf, tr func(string) string, w float64, title string, gray, white func()) {
	gray()
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(w, 7, tr(title), "1", 1, "C", true, 0, "")
	white()
}

func tableHeader(pdf *fpdf.Fpdf, tr func(string) string, w float64, gray, white func()) {
	gray()
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(14, 6, tr("No."), "1", 0, "C", true, 0, "")
	pdf.CellFormat(w-14, 6, tr("Description"), "1", 1, "L", true, 0, "")
	white()
}

// numberedRow renders one "No. | (bold title + description)" row with a shared
// border height, paginating when it would overflow the page.
func numberedRow(pdf *fpdf.Fpdf, tr func(string) string, left, w, y, lineH, bottom float64, n int, title, desc string, gray, white func()) float64 {
	const noW = 14.0
	descW := w - noW
	desc = strings.TrimSpace(desc)

	pdf.SetFont("Arial", "B", 10)
	var titleLines []string
	if t := strings.TrimSpace(title); t != "" {
		titleLines = pdf.SplitText(tr(t), descW-2)
	}
	pdf.SetFont("Arial", "", 10)
	descLines := pdf.SplitText(tr(desc), descW-2)
	h := float64(len(titleLines)+len(descLines))*lineH + 2
	if h < 8 {
		h = 8
	}

	// Page break: redraw the column header on the new page so the table reads.
	if y+h > bottom {
		pdf.AddPage()
		tableHeader(pdf, tr, w, gray, white)
		y = pdf.GetY()
	}

	pdf.Rect(left, y, noW, h, "D")
	pdf.SetXY(left, y+1)
	pdf.CellFormat(noW, lineH, strconv.Itoa(n), "", 0, "C", false, 0, "")

	pdf.Rect(left+noW, y, descW, h, "D")
	yy := y + 1
	if len(titleLines) > 0 {
		pdf.SetFont("Arial", "B", 10)
		pdf.SetXY(left+noW+1, yy)
		pdf.MultiCell(descW-2, lineH, strings.Join(titleLines, "\n"), "", "L", false)
		yy += float64(len(titleLines)) * lineH
	}
	pdf.SetFont("Arial", "", 10)
	pdf.SetXY(left+noW+1, yy)
	pdf.MultiCell(descW-2, lineH, strings.Join(descLines, "\n"), "", "L", false)
	return y + h
}

func needPage(pdf *fpdf.Fpdf, need, bottom float64) bool {
	return pdf.GetY()+need > bottom
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
