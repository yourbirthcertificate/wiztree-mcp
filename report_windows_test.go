package main

import (
	"context"
	"strings"
	"testing"
)

func TestLocalizedReport(t *testing.T) {
	// The two .bin entries represent one file on disk. Nested folder totals overlap.
	fixture := `Gegenereerd door WizTree
Bestandsnaam,Grootte,Toegewezen,Gewijzigd,Attributen,Bestanden,Mappen
"D:\",202,4096,,0,3,2
"D:\a\",202,4096,,16,3,1
"D:\a\b\",100,0,,16,1,0
"D:\a\b\link.bin",100,04096,,32,0,0
"D:\a\résumé,1.BIN",100,4096,,32,0,0
"D:\a\.ENV",2,0,,32,0,0
`
	r, err := readReport(context.Background(), strings.NewReader(fixture), `D:\`, "allocated", 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root.Allocated != 4096 || r.Files != 3 || r.Folders != 2 {
		t.Fatalf("wrong root totals: %+v", r)
	}
	if len(r.LargestFolders) != 1 || r.LargestFolders[0].Path != `D:\a` {
		t.Fatal(r.LargestFolders)
	}
	if r.LargestFiles[0].Path != `D:\a\résumé,1.BIN` {
		t.Fatal(r.LargestFiles)
	}
	if len(r.FileTypes) != 2 || r.FileTypes[0] != (FileType{Extension: ".bin", Files: 2, Size: 200, Allocated: 4096}) || r.FileTypes[1].Extension != "" {
		t.Fatal(r.FileTypes)
	}
}

func TestIncompleteExport(t *testing.T) {
	for _, body := range []string{
		"a,b,c,d,e,f,g\n",
		"a,b,c,d,e,f,g\n\"D:\\\\\",12",
		"a,b,c,d,e,f,g\n\"D:\\\\\",xx,0,,0,0,0\n",
	} {
		if _, err := readReport(context.Background(), strings.NewReader(body), `D:\`, "size", 10); err == nil {
			t.Fatalf("accepted malformed export %q", body)
		}
	}
}
