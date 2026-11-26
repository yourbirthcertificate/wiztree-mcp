package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Entry struct {
	Path      string `json:"path"`
	Size      int64  `json:"size_bytes"`
	Allocated int64  `json:"allocated_bytes"`
}

type FileType struct {
	Extension string `json:"extension"`
	Files     int64  `json:"files"`
	Size      int64  `json:"size_bytes"`
	Allocated int64  `json:"allocated_bytes"`
}

type Report struct {
	Root           Entry      `json:"root"`
	Files          int64      `json:"files"`
	Folders        int64      `json:"folders"`
	RankBy         string     `json:"rank_by"`
	LargestFiles   []Entry    `json:"largest_files"`
	LargestFolders []Entry    `json:"largest_immediate_subfolders"`
	FileTypes      []FileType `json:"largest_file_types"`
}

func weight(metric string, size, allocated int64) int64 {
	if metric == "size" {
		return size
	}
	return allocated
}

// Keep only the requested number of rows, even when the CSV spans a whole drive.
func insertTop(entries []Entry, item Entry, metric string, limit int) []Entry {
	w := weight(metric, item.Size, item.Allocated)
	before := func(other Entry) bool {
		ow := weight(metric, other.Size, other.Allocated)
		return w > ow || (w == ow && item.Path < other.Path)
	}
	if len(entries) == limit && !before(entries[limit-1]) {
		return entries
	}
	pos := sort.Search(len(entries), func(i int) bool { return before(entries[i]) })
	if len(entries) < limit {
		entries = append(entries, Entry{})
	}
	copy(entries[pos+1:], entries[pos:])
	entries[pos] = item
	return entries
}

func number(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid WizTree byte count %q", s)
	}
	return n, nil
}

func readReport(ctx context.Context, input io.Reader, root, metric string, limit int) (*Report, error) {
	buf := bufio.NewReader(input)
	// WizTree adds a localized donor notice before the CSV header on some installs.
	// Its fixed seven-column layout is independent of the display language.
	for line := 0; ; line++ {
		raw, err := buf.ReadString('\n')
		if err != nil {
			return nil, errors.New("WizTree export is missing its header")
		}
		header, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).Read()
		if err == nil && len(header) == 7 {
			break
		}
		if line == 1 {
			return nil, errors.New("unexpected WizTree export header")
		}
	}
	rows := csv.NewReader(buf)
	rows.FieldsPerRecord = 7
	rows.ReuseRecord = true
	report := &Report{RankBy: metric, LargestFiles: []Entry{}, LargestFolders: []Entry{}, FileTypes: []FileType{}}
	types := make(map[string]*FileType)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := rows.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading WizTree CSV: %w", err)
		}
		size, err := number(row[1])
		if err != nil {
			return nil, err
		}
		allocated, err := number(row[2])
		if err != nil {
			return nil, err
		}
		name := filepath.Clean(row[0])
		if !filepath.IsAbs(name) {
			return nil, fmt.Errorf("unexpected relative path in WizTree export: %q", name)
		}
		isFolder := strings.HasSuffix(row[0], `\`)
		// Extra hard-link directory entries are marked by a leading zero.
		if !isFolder && len(row[2]) > 1 && row[2][0] == '0' {
			allocated = 0
		}
		entry := Entry{Path: name, Size: size, Allocated: allocated}
		if isFolder {
			if strings.EqualFold(name, root) {
				report.Root = entry
				continue
			}
			report.Folders++
			if strings.EqualFold(filepath.Dir(name), root) {
				report.LargestFolders = insertTop(report.LargestFolders, entry, metric, limit)
			}
			continue
		}
		report.Files++
		report.LargestFiles = insertTop(report.LargestFiles, entry, metric, limit)
		ext := filepath.Ext(name)
		if ext == filepath.Base(name) || ext == "." {
			ext = ""
		}
		ext = strings.ToLower(ext)
		group := types[ext]
		if group == nil {
			group = &FileType{Extension: ext}
			types[ext] = group
		}
		group.Files++
		group.Size += size
		group.Allocated += allocated
	}
	if report.Root.Path == "" {
		return nil, fmt.Errorf("WizTree did not export the requested root %q; check access to the folder", root)
	}
	for _, group := range types {
		report.FileTypes = append(report.FileTypes, *group)
	}
	sort.Slice(report.FileTypes, func(i, j int) bool {
		a, b := report.FileTypes[i], report.FileTypes[j]
		wa, wb := weight(metric, a.Size, a.Allocated), weight(metric, b.Size, b.Allocated)
		return wa > wb || (wa == wb && a.Extension < b.Extension)
	})
	if len(report.FileTypes) > limit {
		report.FileTypes = report.FileTypes[:limit:limit]
	}
	return report, nil
}
