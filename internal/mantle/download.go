package mantle

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// withinDir reports whether candidate resolves to a path inside base.
func withinDir(base, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// errCancelled signals that a download was aborted via task cancellation.
var errCancelled = fmt.Errorf("download cancelled")

// downloadFile downloads url into localPath, resuming from an existing .part
// file when present. progress is called as bytes arrive with (downloaded,
// totalSize) for this file (totalSize <= 0 when unknown). The download honors
// task cancellation and returns the final file size on success.
func downloadFile(task *Task, url, localPath string, progress func(downloaded, total int64)) (int64, error) {
	partPath := localPath + ".part"

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory: %w", err)
	}

	// Check for partial download for resume
	var existingSize int64
	if fi, err := os.Stat(partPath); err == nil {
		existingSize = fi.Size()
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Total size from Content-Range or Content-Length
	var totalSize int64
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		if parts := strings.Split(cr, "/"); len(parts) == 2 {
			fmt.Sscanf(parts[1], "%d", &totalSize)
		}
	} else {
		totalSize = resp.ContentLength
	}

	flag := os.O_CREATE | os.O_WRONLY
	if existingSize > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(partPath, flag, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to open output file: %w", err)
	}
	defer f.Close()

	downloaded := existingSize
	buf := make([]byte, 32*1024)

	for {
		select {
		case <-task.Done():
			f.Close()
			os.Remove(partPath)
			return 0, errCancelled
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return 0, fmt.Errorf("write error: %w", writeErr)
			}
			downloaded += int64(n)
			if progress != nil {
				progress(downloaded, totalSize)
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, fmt.Errorf("read error: %w", readErr)
		}
	}

	f.Close()
	if err := os.Rename(partPath, localPath); err != nil {
		return 0, fmt.Errorf("failed to finalize file: %w", err)
	}
	return downloaded, nil
}

// StartDownload begins downloading a single model file from HuggingFace.
// Runs in a goroutine, emitting progress events through the task.
func (tm *TaskManager) StartDownload(modelID, filename, modelsDir string) *Task {
	task := tm.CreateTask("download", "", "", modelID)

	go func() {
		if strings.ContainsAny(filename, "/\\") || filename == ".." || filename == "." {
			task.UpdateProgress(TaskFailed, "invalid filename", 0)
			return
		}
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", modelID, filename)
		localPath := filepath.Join(modelsDir, strings.ReplaceAll(modelID, "/", "_"), filename)
		if !withinDir(modelsDir, localPath) {
			task.UpdateProgress(TaskFailed, "invalid filename: path escapes models directory", 0)
			return
		}

		task.UpdateProgress(TaskRunning, fmt.Sprintf("Downloading %s/%s...", modelID, filename), 0)

		lastPct := -1
		downloaded, err := downloadFile(task, url, localPath, func(downloaded, total int64) {
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct != lastPct {
					lastPct = pct
					task.UpdateProgress(TaskRunning,
						fmt.Sprintf("Downloading %s... %d%%", filename, pct), pct)
				}
			} else if downloaded/1024/1024 != int64(lastPct) {
				lastPct = int(downloaded / 1024 / 1024)
				task.UpdateProgress(TaskRunning,
					fmt.Sprintf("Downloading %s... (%d MB)", filename, downloaded/1024/1024), -1)
			}
		})
		if err == errCancelled {
			return
		}
		if err != nil {
			task.UpdateProgress(TaskFailed, err.Error(), 0)
			return
		}

		gb := float64(downloaded) / 1024 / 1024 / 1024
		task.UpdateProgress(TaskCompleted,
			fmt.Sprintf("Downloaded %s (%.2f GB)", filename, gb), 100)
	}()

	return task
}

// StartRepoDownload downloads every file in a HuggingFace repo into a single
// folder, preserving sub-paths. Progress is reported as an aggregate percent
// across the repo's total bytes. Files already present at full size are skipped.
func (tm *TaskManager) StartRepoDownload(modelID, modelsDir string) *Task {
	task := tm.CreateTask("download", "", "", modelID)

	go func() {
		task.UpdateProgress(TaskRunning, fmt.Sprintf("Listing %s...", modelID), 0)

		files, err := ListHFFiles(modelID)
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to list repo files: %v", err), 0)
			return
		}
		if len(files) == 0 {
			task.UpdateProgress(TaskFailed, "Repo has no downloadable files", 0)
			return
		}

		var totalBytes int64
		for _, fl := range files {
			totalBytes += fl.Size
		}

		repoDir := filepath.Join(modelsDir, strings.ReplaceAll(modelID, "/", "_"))
		var completedBytes int64
		lastPct := -1

		for i, fl := range files {
			localPath := filepath.Join(repoDir, filepath.FromSlash(fl.Path))
			if !withinDir(repoDir, localPath) {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("%s: path traversal detected", fl.Path), 0)
				return
			}

			// Skip files already downloaded at the expected size.
			if fi, statErr := os.Stat(localPath); statErr == nil && (fl.Size <= 0 || fi.Size() == fl.Size) {
				completedBytes += fl.Size
				continue
			}

			url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", modelID, fl.Path)
			base := completedBytes
			fileIdx := i + 1
			n, err := downloadFile(task, url, localPath, func(downloaded, total int64) {
				if totalBytes > 0 {
					pct := int((base + downloaded) * 100 / totalBytes)
					if pct != lastPct {
						lastPct = pct
						task.UpdateProgress(TaskRunning,
							fmt.Sprintf("Downloading %s (%d/%d)... %d%%", fl.Path, fileIdx, len(files), pct), pct)
					}
				}
			})
			if err == errCancelled {
				return
			}
			if err != nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("%s: %v", fl.Path, err), 0)
				return
			}
			completedBytes += n
		}

		gb := float64(totalBytes) / 1024 / 1024 / 1024
		task.UpdateProgress(TaskCompleted,
			fmt.Sprintf("Downloaded %s (%d files, %.2f GB)", modelID, len(files), gb), 100)
	}()

	return task
}
