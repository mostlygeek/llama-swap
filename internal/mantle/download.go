package mantle

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StartDownload begins downloading a GGUF model file from HuggingFace.
// Runs in a goroutine, emitting progress events through the task.
func (tm *TaskManager) StartDownload(modelID, filename, modelsDir string) *Task {
	task := tm.CreateTask("download", "", "", modelID)

	go func() {
		url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", modelID, filename)
		localPath := filepath.Join(modelsDir, strings.ReplaceAll(modelID, "/", "_"), filename)
		partPath := localPath + ".part"

		task.UpdateProgress(TaskRunning, fmt.Sprintf("Downloading %s/%s...", modelID, filename), 0)

		// Create parent directory
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to create directory: %v", err), 0)
			return
		}

		// Check for partial download for resume
		var existingSize int64
		if fi, err := os.Stat(partPath); err == nil {
			existingSize = fi.Size()
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to create request: %v", err), 0)
			return
		}

		if existingSize > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
			task.UpdateProgress(TaskRunning, fmt.Sprintf("Resuming from %d MB", existingSize/1024/1024),
				int(float64(existingSize)/float64(existingSize+1024*1024)*100))
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Download request failed: %v", err), 0)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Download returned status %d", resp.StatusCode), 0)
			return
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

		// Open .part file for appending or writing
		flag := os.O_CREATE | os.O_WRONLY
		if existingSize > 0 {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}
		f, err := os.OpenFile(partPath, flag, 0644)
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to open output file: %v", err), 0)
			return
		}
		defer f.Close()

		downloaded := existingSize
		buf := make([]byte, 32*1024)
		lastPct := -1

		for {
			// Check cancellation
			select {
			case <-task.Done():
				f.Close()
				os.Remove(partPath)
				return
			default:
			}

			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := f.Write(buf[:n]); writeErr != nil {
					task.UpdateProgress(TaskFailed, fmt.Sprintf("Write error: %v", writeErr), 0)
					return
				}
				downloaded += int64(n)

				if totalSize > 0 {
					pct := int(downloaded * 100 / totalSize)
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
			}

			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Read error: %v", readErr), 0)
				return
			}
		}

		// Rename .part to final filename
		f.Close()
		if err := os.Rename(partPath, localPath); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to finalize file: %v", err), 0)
			return
		}

		gb := float64(downloaded) / 1024 / 1024 / 1024
		task.UpdateProgress(TaskCompleted,
			fmt.Sprintf("Downloaded %s (%.2f GB)", filename, gb), 100)
	}()

	return task
}
