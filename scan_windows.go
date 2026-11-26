package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type Drive struct {
	Path  string  `json:"path"`
	Type  string  `json:"type"`
	Total *uint64 `json:"total_bytes,omitempty"`
	Free  *uint64 `json:"free_bytes_available,omitempty"`
}
type Drives struct {
	Drives []Drive `json:"drives"`
}

func drives() (Drives, error) {
	mask, err := windows.GetLogicalDrives()
	out := Drives{Drives: []Drive{}}
	if err != nil {
		return out, err
	}
	kinds := []string{"unknown", "unavailable", "removable", "fixed", "network", "optical", "ramdisk"}
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		path := fmt.Sprintf("%c:\\", 'A'+i)
		p, _ := windows.UTF16PtrFromString(path)
		kind := windows.GetDriveType(p)
		d := Drive{Path: path, Type: "unknown"}
		if int(kind) < len(kinds) {
			d.Type = kinds[kind]
		}
		if d.Type == "fixed" || d.Type == "ramdisk" {
			var total, free uint64
			if windows.GetDiskFreeSpaceEx(p, &free, &total, nil) == nil {
				d.Total = &total
				d.Free = &free
			}
		}
		out.Drives = append(out.Drives, d)
	}
	return out, nil
}

func wiztreePath() (string, error) {
	if p := os.Getenv("WIZTREE_PATH"); p != "" {
		if info, err := os.Stat(p); !filepath.IsAbs(p) || err != nil || info.IsDir() {
			return "", errors.New("WIZTREE_PATH must be the absolute path of the WizTree executable")
		}
		return p, nil
	}
	for _, name := range []string{"WizTree64.exe", "WizTree.exe"} {
		for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86} {
			base, err := windows.KnownFolderPath(id, 0)
			if err != nil {
				continue
			}
			p := filepath.Join(base, "WizTree", name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("install WizTree or set WIZTREE_PATH to the full path of WizTree64.exe")
}

// adminRequested reports whether WIZTREE_ADMIN asks WizTree to elevate. When it
// is set, WizTree can read the NTFS master file table, which is much faster on
// whole drives, but Windows shows a UAC prompt for every scan.
func adminRequested() (bool, error) {
	v := os.Getenv("WIZTREE_ADMIN")
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, errors.New("WIZTREE_ADMIN must be 1 or 0")
	}
	return b, nil
}

type ScanInput struct {
	Path   string `json:"path" jsonschema:"Absolute Windows drive or directory to scan (for example C:\\\\Users or D:)."`
	RankBy string `json:"rank_by,omitempty" jsonschema:"Ranking metric: allocated (default) or size for logical file length."`
	Limit  *int   `json:"limit,omitempty" jsonschema:"Rows per ranking, 1 to 100. Defaults to 10."`
}
type ScanID struct {
	ID string `json:"scan_id" jsonschema:"ID returned by scan."`
}
type GetInput struct {
	ID   string `json:"scan_id" jsonschema:"ID returned by scan."`
	Wait *int   `json:"wait_seconds,omitempty" jsonschema:"Wait up to 0 to 15 seconds for completion. Defaults to 5."`
}

type ScanResult struct {
	ID     string  `json:"scan_id"`
	Path   string  `json:"path"`
	State  string  `json:"state"`
	Error  string  `json:"error,omitempty"`
	Report *Report `json:"report,omitempty"`
}

type job struct {
	id, path string
	done     chan struct{}
	cancel   context.CancelFunc
	// The worker publishes final by closing done after writing it.
	final ScanResult
}

func (j *job) result(ctx context.Context, wait time.Duration) (ScanResult, error) {
	select {
	case <-j.done:
		return j.final, nil
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-j.done:
		return j.final, nil
	case <-ctx.Done():
		return ScanResult{}, ctx.Err()
	case <-timer.C:
		return ScanResult{ID: j.id, Path: j.path, State: "running"}, nil
	}
}

type scans struct {
	ctx  context.Context
	mu   sync.Mutex
	jobs []*job
	wg   sync.WaitGroup
}

func (s *scans) start(ctx context.Context, input ScanInput) (ScanResult, error) {
	if input.RankBy == "" {
		input.RankBy = "allocated"
	}
	if input.RankBy != "size" && input.RankBy != "allocated" {
		return ScanResult{}, errors.New("rank_by must be allocated or size")
	}
	limit := 10
	if input.Limit != nil {
		limit = *input.Limit
	}
	if limit < 1 || limit > 100 {
		return ScanResult{}, errors.New("limit must be between 1 and 100")
	}
	path := filepath.FromSlash(input.Path)
	if len(path) == 2 && path[1] == ':' {
		path += `\`
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\.\`) || strings.HasPrefix(path, `\\?\`) {
		return ScanResult{}, errors.New("provide a drive letter, absolute directory or UNC share")
	}
	st, err := os.Stat(path)
	if err != nil {
		return ScanResult{}, err
	}
	if !st.IsDir() {
		return ScanResult{}, errors.New("the scan path must be a directory or drive")
	}
	exe, err := wiztreePath()
	if err != nil {
		return ScanResult{}, err
	}
	admin, err := adminRequested()
	if err != nil {
		return ScanResult{}, err
	}

	s.mu.Lock()
	if n := len(s.jobs); n > 0 {
		select {
		case <-s.jobs[n-1].done:
		default:
			s.mu.Unlock()
			return ScanResult{}, errors.New("a scan is already running; wait for it to finish before starting another")
		}
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return ScanResult{}, s.ctx.Err()
	}
	scanCtx, cancel := context.WithTimeout(s.ctx, time.Hour)
	j := &job{id: rand.Text(), path: path, done: make(chan struct{}), cancel: cancel}
	if len(s.jobs) >= 8 {
		s.jobs = s.jobs[1:]
	}
	s.jobs = append(s.jobs, j)
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer close(j.done)
		defer cancel()
		report, err := runScan(scanCtx, exe, path, input.RankBy, limit, admin)
		j.final = ScanResult{ID: j.id, Path: path, State: "complete", Report: report}
		if err != nil {
			j.final.State = "failed"
			j.final.Error = err.Error()
			if errors.Is(scanCtx.Err(), context.Canceled) {
				j.final.State = "cancelled"
				j.final.Error = ""
			}
			if errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
				j.final.Error = "scan exceeded one hour"
			}
		}
	}()
	return j.result(ctx, time.Second)
}

func (s *scans) find(id string) (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.id == id {
			return j, nil
		}
	}
	return nil, errors.New("unknown or expired scan_id; start a new scan")
}

// wiztreeArgs builds the WizTree command line. Without admin, /admin=0 stops
// WizTree from showing a UAC prompt, at the cost of the slower non-MFT scan.
func wiztreeArgs(path, export, configDir string, admin bool) []string {
	elevate := "/admin=0"
	if admin {
		elevate = "/admin=1"
	}
	return []string{path, elevate, "/export=" + export, "/configfolder=" + configDir,
		"/exportfolders=1", "/exportfiles=1", "/exportdrivecapacity=0", "/exportmaxdepth=0",
		"/exportmftrecno=0", "/exportallsizes=0", "/exportsplitfilename=0", "/exportpercentofparent=0", "/exportalldates=0"}
}

func runScan(ctx context.Context, exe, path, metric string, limit int, admin bool) (*Report, error) {
	dir, err := os.MkdirTemp("", "wiztree-mcp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	export := filepath.Join(dir, "scan.csv")
	cmd := exec.CommandContext(ctx, exe, wiztreeArgs(path, export, dir, admin)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("WizTree export failed: %w", err)
	}
	f, err := os.Open(export)
	if err != nil {
		return nil, fmt.Errorf("WizTree produced no readable CSV for %s: %w", path, err)
	}
	defer f.Close()
	return readReport(ctx, f, path, metric, limit)
}
