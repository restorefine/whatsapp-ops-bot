package editorcounts

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cell struct {
	text   string
	number bool
}

func text(s string) cell { return cell{text: s} }
func number(n int) cell  { return cell{text: strconv.Itoa(n), number: true} }

// Workbook creates a real XLSX file. Untrusted task names are inline strings,
// never formulas, even when they start with =, +, - or @.
func Workbook(report Report) ([]byte, error) {
	summary := [][]cell{{text("Editor"), text("New-cycle videos"), text("Full 20-video batches"), text("Current batch"), text("Current progress"), text("Remaining"), text("Evidence issues"), text("Checked at (UTC)"), text("Historical videos"), text("New cycles started (UTC)")}}
	tasks := [][]cell{{text("Editor"), text("Batch"), text("Position"), text("Task ID"), text("Video task"), text("List"), text("ClickUp proof"), text("Completed at (UTC)"), text("Recorded at (UTC)"), text("Original status"), text("Initial historical import"), text("Audit issue")}}
	notes := [][]cell{{text("Editor"), text("Note")}, {text("All editors"), text("One completed top-level task in the editor's personal ClickUp list counts as one video. No monthly reset. A full batch is not proof of payment.")}, {text("All editors"), text("First scan starts at 0/20. Existing completed tasks are historical evidence (batch 0), excluded from new cycles and automatic alerts. Missing, reopened or reassigned tasks retain their original record and are flagged.")}}
	for _, e := range report.Editors {
		summary = append(summary, []cell{text(e.Name), number(e.Total), number(e.FullBatches), number(e.CurrentBatch), number(e.CurrentCount), number(e.Remaining), number(e.ReviewCount), text(report.CheckedAt.Format(time.RFC3339)), number(e.HistoricalTotal), text(e.StartedAt.Format(time.RFC3339))})
		for _, r := range e.Records {
			tasks = append(tasks, []cell{text(e.Name), number(r.Batch), number(r.Position), text(r.TaskID), text(r.Title), text(r.List), text(r.URL), text(r.CompletedAt.Format(time.RFC3339)), text(r.RecordedAt.Format(time.RFC3339)), text(r.Status), text(strconv.FormatBool(r.Imported)), text(r.Issue)})
		}
		for _, warning := range e.Warnings {
			notes = append(notes, []cell{text(e.Name), text(warning)})
		}
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	write := func(name, body string) error {
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(xml.Header + body))
		return err
	}
	parts := [][2]string{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet3.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Summary" sheetId="1" r:id="rId1"/><sheet name="Video evidence" sheetId="2" r:id="rId2"/><sheet name="Counting notes" sheetId="3" r:id="rId3"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet3.xml"/></Relationships>`},
	}
	for _, p := range parts {
		if err := write(p[0], p[1]); err != nil {
			return nil, err
		}
	}
	var proofLinks strings.Builder
	proofLinks.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for row := 1; row < len(tasks); row++ {
		fmt.Fprintf(&proofLinks, `<Relationship Id="proof%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="`, row)
		_ = xml.EscapeText(&proofLinks, []byte(tasks[row][6].text))
		proofLinks.WriteString(`" TargetMode="External"/>`)
	}
	proofLinks.WriteString(`</Relationships>`)
	if err := write("xl/worksheets/_rels/sheet2.xml.rels", proofLinks.String()); err != nil {
		return nil, err
	}
	for i, rows := range [][][]cell{summary, tasks, notes} {
		var sheet strings.Builder
		sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><sheetData>`)
		for rowIndex, row := range rows {
			fmt.Fprintf(&sheet, `<row r="%d">`, rowIndex+1)
			for col, c := range row {
				ref := string(rune('A'+col)) + strconv.Itoa(rowIndex+1)
				if c.number {
					fmt.Fprintf(&sheet, `<c r="%s"><v>%s</v></c>`, ref, c.text)
					continue
				}
				fmt.Fprintf(&sheet, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">`, ref)
				safe := strings.Map(func(r rune) rune {
					if (r >= 32 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || (r >= 0x10000 && r <= 0x10ffff) || r == '\n' || r == '\r' || r == '\t' {
						return r
					}
					return -1
				}, c.text)
				chars := []rune(safe)
				if len(chars) > 32767 {
					safe = string(chars[:32767])
				}
				_ = xml.EscapeText(&sheet, []byte(safe))
				sheet.WriteString(`</t></is></c>`)
			}
			sheet.WriteString(`</row>`)
		}
		sheet.WriteString(`</sheetData>`)
		if len(rows) > 1 {
			fmt.Fprintf(&sheet, `<autoFilter ref="A1:%s%d"/>`, string(rune('A'+len(rows[0])-1)), len(rows))
		}
		if i == 1 && len(rows) > 1 {
			sheet.WriteString(`<hyperlinks>`)
			for row := 1; row < len(rows); row++ {
				fmt.Fprintf(&sheet, `<hyperlink ref="G%d" r:id="proof%d"/>`, row+1, row)
			}
			sheet.WriteString(`</hyperlinks>`)
		}
		sheet.WriteString(`</worksheet>`)
		if err := write(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheet.String()); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
