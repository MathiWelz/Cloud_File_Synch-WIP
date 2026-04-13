// Package sync implements the bidirectional cloud ↔ local file synchronisation engine.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudsync/config"
	"cloudsync/providers"
)

// PromptFn is called when user confirmation is needed (e.g. low disk space).
// It should print the message and return true if the user chooses to continue.
type PromptFn func(message string) bool

// ─── Report ────────────────────────────────────────────────────────

// SyncResult summarises the outcome for a single provider.
type SyncResult struct {
	ProviderName string
	Downloaded   []string
	Uploaded     []string
	Skipped      []string
	Errors       []string
}

// Report is the aggregate result across all providers.
type Report struct {
	StartTime time.Time
	EndTime   time.Time
	Results   []SyncResult
}

// Summary returns a human-readable text report.
func (r *Report) Summary() string {
	line := strings.Repeat("═", 58)
	thin := strings.Repeat("─", 58)
	var sb strings.Builder

	sb.WriteString("\n" + line + "\n")
	sb.WriteString("  📊  CloudSync Report\n")
	sb.WriteString(line + "\n")
	sb.WriteString(fmt.Sprintf("  Started:  %s\n", r.StartTime.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("  Finished: %s\n", r.EndTime.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("  Duration: %s\n", r.EndTime.Sub(r.StartTime).Round(time.Second)))
	sb.WriteString(thin + "\n")

	for _, res := range r.Results {
		sb.WriteString(fmt.Sprintf("\n  📁 %s\n", res.ProviderName))
		sb.WriteString(fmt.Sprintf("     ⬇  Downloaded : %d file(s)\n", len(res.Downloaded)))
		sb.WriteString(fmt.Sprintf("     ⬆  Uploaded   : %d file(s)\n", len(res.Uploaded)))
		sb.WriteString(fmt.Sprintf("     ⏭  Skipped    : %d file(s)\n", len(res.Skipped)))
		if len(res.Errors) > 0 {
			sb.WriteString(fmt.Sprintf("     ❌ Errors     : %d\n", len(res.Errors)))
			for _, e := range res.Errors {
				sb.WriteString(fmt.Sprintf("        • %s\n", e))
			}
		}
	}
	sb.WriteString("\n" + line + "\n")
	return sb.String()
}

// HTMLSummary returns an HTML version for email notifications.
func (r *Report) HTMLSummary() string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><body style="font-family:Arial,sans-serif;color:#222">`)
	sb.WriteString(`<h2>☁️ CloudSync Report</h2>`)
	sb.WriteString(fmt.Sprintf(`<p><b>Duration:</b> %s &nbsp; (<b>%s</b> → <b>%s</b>)</p>`,
		r.EndTime.Sub(r.StartTime).Round(time.Second),
		r.StartTime.Format("15:04:05"),
		r.EndTime.Format("15:04:05")))

	for _, res := range r.Results {
		sb.WriteString(fmt.Sprintf(`<h3 style="border-bottom:1px solid #ccc;padding-bottom:4px">📁 %s</h3>`, res.ProviderName))
		sb.WriteString(`<table style="border-collapse:collapse;width:100%">`)
		row := func(icon, label string, count int, color string) {
			sb.WriteString(fmt.Sprintf(
				`<tr><td style="padding:4px 8px">%s</td><td style="padding:4px 8px;font-weight:bold;color:%s">%d</td><td style="padding:4px 8px;color:#666">%s</td></tr>`,
				icon, color, count, label))
		}
		row("⬇", "Downloaded", len(res.Downloaded), "#1a7f37")
		row("⬆", "Uploaded", len(res.Uploaded), "#1a5fb4")
		row("⏭", "Skipped (unchanged)", len(res.Skipped), "#888")
		if len(res.Errors) > 0 {
			row("❌", "Errors", len(res.Errors), "#c0392b")
			sb.WriteString(`<tr><td colspan="3"><ul>`)
			for _, e := range res.Errors {
				sb.WriteString(fmt.Sprintf(`<li style="color:#c0392b">%s</li>`, e))
			}
			sb.WriteString(`</ul></td></tr>`)
		}
		sb.WriteString(`</table>`)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

// ─── Syncer ────────────────────────────────────────────────────────

// Syncer orchestrates sync operations across all configured providers.
type Syncer struct {
	cfg       *config.Config
	providers []providers.Provider
}

// New creates a Syncer ready to run.
func New(cfg *config.Config, ps []providers.Provider) *Syncer {
	return &Syncer{cfg: cfg, providers: ps}
}

// Run executes the full sync cycle and returns a report.
func (s *Syncer) Run(ctx context.Context, prompt PromptFn) (*Report, error) {
	report := &Report{StartTime: time.Now()}
	var lastErr error

	for _, p := range s.providers {
		if ctx.Err() != nil {
			break
		}
		fmt.Printf("┌─ %s\n", p.Name())
		res, err := s.syncProvider(ctx, p, prompt)
		if err != nil {
			fmt.Printf("└─ ❌ %v\n\n", err)
			res.Errors = append(res.Errors, err.Error())
			lastErr = err
		} else {
			fmt.Printf("└─ ✅ done  ⬇%d  ⬆%d  ⏭%d  ❌%d\n\n",
				len(res.Downloaded), len(res.Uploaded), len(res.Skipped), len(res.Errors))
		}
		report.Results = append(report.Results, res)
	}

	report.EndTime = time.Now()
	return report, lastErr
}

// ─── Per-provider sync ─────────────────────────────────────────────

func (s *Syncer) syncProvider(ctx context.Context, p providers.Provider, prompt PromptFn) (SyncResult, error) {
	res := SyncResult{ProviderName: p.Name()}

	// Load persisted state (last known file metadata for change detection)
	cloudState, err := loadState(s.cfg.StateDir, p.Name()+".cloud")
	if err != nil {
		fmt.Printf("│  ⚠️  state load error (treating all files as new): %v\n", err)
		cloudState = State{}
	}
	localState, err := loadState(s.cfg.StateDir, p.Name()+".local")
	if err != nil {
		localState = State{}
	}

	newCloudState := State{}
	newLocalState := State{}

	// ── Cloud → Local ─────────────────────────────────────────────
	if p.SyncDirection() != "local-to-cloud" {
		fmt.Println("│  ☁ → 💾  checking cloud...")

		remoteFiles, err := p.ListFiles(ctx)
		if err != nil {
			return res, fmt.Errorf("list remote files: %w", err)
		}
		fmt.Printf("│     %d remote file(s) found\n", len(remoteFiles))

		for _, rf := range remoteFiles {
			if ctx.Err() != nil {
				break
			}
			if s.isExcluded(rf.Path) {
				res.Skipped = append(res.Skipped, rf.Path)
				continue
			}

			newCloudState[rf.Path] = toFileState(rf)
			localPath := filepath.Join(p.LocalDest(), filepath.FromSlash(rf.Path))

			// Skip if unchanged AND the local copy exists
			if !hasChanged(rf, cloudState[rf.Path]) {
				if _, err := os.Stat(localPath); err == nil {
					res.Skipped = append(res.Skipped, rf.Path)
					continue
				}
			}

			// Max file size guard
			if s.cfg.Sync.MaxFileSizeMB > 0 && rf.Size > s.cfg.Sync.MaxFileSizeMB*1024*1024 {
				msg := fmt.Sprintf("skip (too large): %s (%.1f MB)", rf.Path, mbOf(rf.Size))
				fmt.Printf("│     ⏭  %s\n", msg)
				res.Skipped = append(res.Skipped, rf.Path)
				continue
			}

			// Disk space guard
			proceed, err := s.checkDiskSpace(p.LocalDest(), rf.Size, prompt, rf.Path)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("disk check: %v", err))
				continue
			}
			if !proceed {
				res.Skipped = append(res.Skipped, rf.Path)
				continue
			}

			fmt.Printf("│     ⬇  %s (%.1f KB)\n", rf.Path, float64(rf.Size)/1024)
			if err := p.Download(ctx, rf.Path, localPath); err != nil {
				e := fmt.Sprintf("download %s: %v", rf.Path, err)
				fmt.Printf("│     ❌ %s\n", e)
				res.Errors = append(res.Errors, e)
			} else {
				res.Downloaded = append(res.Downloaded, rf.Path)
			}
		}
	}

	// ── Local → Cloud ─────────────────────────────────────────────
	if p.SyncDirection() != "cloud-to-local" {
		fmt.Println("│  💾 → ☁  checking local...")

		localFiles, err := s.listLocalFiles(p.LocalDest())
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("│     local dir not found yet: %s\n", p.LocalDest())
			} else {
				return res, fmt.Errorf("list local files: %w", err)
			}
		}
		fmt.Printf("│     %d local file(s) found\n", len(localFiles))

		for _, lf := range localFiles {
			if ctx.Err() != nil {
				break
			}
			if s.isExcluded(lf.Path) {
				res.Skipped = append(res.Skipped, "[local] "+lf.Path)
				continue
			}

			newLocalState[lf.Path] = toFileState(lf)

			if !hasChanged(lf, localState[lf.Path]) {
				res.Skipped = append(res.Skipped, "[local] "+lf.Path)
				continue
			}

			fullLocal := filepath.Join(p.LocalDest(), filepath.FromSlash(lf.Path))
			fmt.Printf("│     ⬆  %s (%.1f KB)\n", lf.Path, float64(lf.Size)/1024)
			if err := p.Upload(ctx, fullLocal, lf.Path); err != nil {
				e := fmt.Sprintf("upload %s: %v", lf.Path, err)
				fmt.Printf("│     ❌ %s\n", e)
				res.Errors = append(res.Errors, e)
			} else {
				res.Uploaded = append(res.Uploaded, lf.Path)
			}
		}
	}

	// Persist updated state
	_ = saveState(s.cfg.StateDir, p.Name()+".cloud", newCloudState)
	_ = saveState(s.cfg.StateDir, p.Name()+".local", newLocalState)

	return res, nil
}

// ─── Local file walker ─────────────────────────────────────────────

func (s *Syncer) listLocalFiles(root string) ([]providers.FileInfo, error) {
	var files []providers.FileInfo

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		fi := providers.FileInfo{
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		// Compute SHA-256 for files < 50 MB to enable accurate change detection
		if info.Size() < 50*1024*1024 {
			fi.Checksum, _ = sha256File(path)
		}
		files = append(files, fi)
		return nil
	})
	return files, err
}

// ─── Disk space ────────────────────────────────────────────────────

// checkDiskSpace verifies the download fits within the configured threshold.
// Returns (true, nil) when it is safe to proceed or when the user accepts the risk.
func (s *Syncer) checkDiskSpace(destDir string, fileSize int64, prompt PromptFn, fileName string) (bool, error) {
	// Ensure the destination exists so freeDiskBytes can stat it.
	_ = os.MkdirAll(destDir, 0o750)

	free, err := freeDiskBytes(destDir)
	if err != nil {
		// Cannot determine disk space — proceed optimistically.
		return true, nil
	}

	threshold := s.cfg.Sync.DiskThreshold
	if threshold <= 0 {
		threshold = 0.75
	}

	if free == 0 || float64(fileSize) > float64(free)*threshold {
		msg := fmt.Sprintf(
			"⚠️  Low disk space!\n"+
				"   File    : %s (%.1f MB)\n"+
				"   Free    : %.1f MB\n"+
				"   Would use %.0f%% of remaining space (threshold: %.0f%%)",
			fileName, mbOf(fileSize),
			float64(free)/(1024*1024),
			pct(fileSize, int64(free)),
			threshold*100,
		)
		return prompt(msg), nil
	}
	return true, nil
}

// ─── Exclusion filter ──────────────────────────────────────────────

func (s *Syncer) isExcluded(relPath string) bool {
	base := filepath.Base(relPath)
	for _, pattern := range s.cfg.Sync.ExcludePatterns {
		if matched, err := filepath.Match(pattern, base); err == nil && matched {
			return true
		}
		// Also match against the full relative path
		if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
			return true
		}
	}
	return false
}

// ─── Utilities ─────────────────────────────────────────────────────

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func mbOf(b int64) float64    { return float64(b) / (1024 * 1024) }
func pct(part, total int64) float64 {
	if total == 0 {
		return 100
	}
	return float64(part) / float64(total) * 100
}
