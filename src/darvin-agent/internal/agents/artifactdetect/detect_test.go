package artifactdetect

import (
	"strings"
	"testing"
)

func TestKindForPath(t *testing.T) {
	cases := map[string]string{
		"report.md":    "markdown",
		"data.csv":     "text",
		"script.py":    "code",
		"index.html":   "html",
		"chart.png":    "image",
		"clip.mp4":     "video",
		"book.docx":    "document",
		"sheet.xlsx":   "document",
		"pres.pptx":    "document",
		"file.pdf":     "document",
		"main.js":      "code",
		"/a/b/styles.css": "code",
	}
	for path, want := range cases {
		got, ok := KindForPath(path)
		if !ok || got != want {
			t.Errorf("KindForPath(%q) = %q, %v; want %q, true", path, got, ok, want)
		}
	}
	if _, ok := KindForPath("noext"); ok {
		t.Errorf("KindForPath(noext) should be false")
	}
}

func TestScanDetectsLocalService(t *testing.T) {
	out := Scan("the dev server runs at http://localhost:5173/api now", "")
	if len(out) != 1 || out[0].Kind != "local-service" {
		t.Fatalf("Scan local-service = %+v", out)
	}
	if !strings.Contains(out[0].URL, "localhost:5173") {
		t.Errorf("url = %q", out[0].URL)
	}
}

func TestScanDetectsRemoteImage(t *testing.T) {
	out := Scan("see ![preview](https://example.com/a.png?w=100)", "")
	if len(out) != 1 || out[0].Kind != "image" {
		t.Fatalf("Scan image = %+v", out)
	}
}

func TestScanDetectsFileLink(t *testing.T) {
	out := Scan("see [report](file:///workspace/report.md)", "/workspace")
	if len(out) != 1 || out[0].Kind != "markdown" {
		t.Fatalf("Scan file link = %+v", out)
	}
	if out[0].FilePath != "report.md" {
		t.Errorf("FilePath = %q, want report.md (relativised)", out[0].FilePath)
	}
}

func TestScanDetectsBarePath(t *testing.T) {
	out := Scan("saved to /workspace/data.csv", "/workspace")
	if len(out) != 1 || out[0].Kind != "text" {
		t.Fatalf("Scan bare path = %+v", out)
	}
	if out[0].FilePath != "data.csv" {
		t.Errorf("FilePath = %q", out[0].FilePath)
	}
}

func TestScanDetectsMediaToken(t *testing.T) {
	out := Scan("generated:\nMEDIA: /workspace/chart.png", "/workspace")
	if len(out) != 1 || out[0].Kind != "image" {
		t.Fatalf("Scan media token = %+v", out)
	}
}

func TestScanDedupes(t *testing.T) {
	text := "a http://localhost:3000 b http://localhost:3000 c"
	out := Scan(text, "")
	if len(out) != 1 {
		t.Fatalf("expected 1 deduped local-service, got %d: %+v", len(out), out)
	}
}

func TestArtifactIDStable(t *testing.T) {
	if ArtifactID("report.md") != ArtifactID("report.md") {
		t.Errorf("ArtifactID not stable")
	}
	if ArtifactID("a.py") == ArtifactID("b.py") {
		t.Errorf("ArtifactID collision")
	}
}

func TestScanIgnoresCommonWords(t *testing.T) {
	// A bare "word/word.ext" without a known extension must not match.
	out := Scan("please check the file for more info", "")
	if len(out) != 0 {
		t.Fatalf("common words matched: %+v", out)
	}
}
