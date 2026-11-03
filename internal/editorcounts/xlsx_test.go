package editorcounts

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func checkWorkbook(t *testing.T, data []byte) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 8 {
		t.Fatalf("workbook parts: %d", len(z.File))
	}
	foundProof := false
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r)
		_ = r.Close()
		decoder := xml.NewDecoder(bytes.NewReader(body))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid %s: %v", f.Name, err)
			}
		}
		if f.Name == "xl/worksheets/sheet2.xml" {
			foundProof = strings.Contains(string(body), "app.clickup.com/t/proof") && strings.Contains(string(body), "HYPERLINK") && strings.Contains(string(body), `t="inlineStr"`) && !strings.Contains(string(body), "<f>")
		}
	}
	if !foundProof {
		t.Fatal("evidence missing or task name became an Excel formula")
	}
}
