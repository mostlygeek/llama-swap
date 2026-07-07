package mantle

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartBuild kicks off a container-local build of a llama.cpp fork. It always
// builds into a staging directory and only moves it into place at backendName
// once the build has produced a working llama-server binary, so a failed
// build never disturbs an existing backend. When replaceExisting is true (an
// "update" rebuild), the existing backendName dir is left untouched until the
// new build succeeds, then swapped in; when false (a fresh build), backendName
// must not already exist.
// Runs as a goroutine, emitting progress events via the task.
func (tm *TaskManager) StartBuild(repo, branch, backendName, buildScript, backendsDir string, cmakeArgs []string, replaceExisting bool) *Task {
	task := tm.CreateTask("build", repo, branch, "")

	go func() {
		task.UpdateProgress(TaskRunning, fmt.Sprintf("Building %s@%s...", repo, branch), 0)

		if branch == "" {
			branch = "main"
		}
		if backendName == "" {
			backendName = "build-" + task.ID
		}
		finalDir := filepath.Join(backendsDir, backendName)
		if !replaceExisting {
			if _, err := os.Stat(finalDir); err == nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Backend %q already exists", backendName), 0)
				return
			} else if !os.IsNotExist(err) {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to check output dir: %v", err), 0)
				return
			}
		}
		stagingDir := filepath.Join(backendsDir, backendName+".building-"+task.ID)
		if err := os.MkdirAll(stagingDir, 0755); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to create output dir: %v", err), 0)
			return
		}

		meta, err := json.Marshal(struct {
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
		}{Repo: repo, Branch: branch})
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to encode build metadata: %v", err), 0)
			os.RemoveAll(stagingDir)
			return
		}
		if err := os.WriteFile(filepath.Join(stagingDir, "meta.json"), meta, 0644); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to write build metadata: %v", err), 0)
			os.RemoveAll(stagingDir)
			return
		}

		args := []string{
			buildScript,
			"--repo", repo,
			"--branch", branch,
			"--out", stagingDir,
		}
		for _, arg := range cmakeArgs {
			args = append(args, "--cmake-arg", arg)
		}
		cmd := exec.Command("bash", args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to get stdout: %v", err), 0)
			os.RemoveAll(stagingDir)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to get stderr: %v", err), 0)
			os.RemoveAll(stagingDir)
			return
		}

		if err := cmd.Start(); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to start build: %v", err), 0)
			os.RemoveAll(stagingDir)
			return
		}

		// Read stdout for progress. Every line is also forwarded to tm.log so
		// the full build output is visible in the container logs / /logs
		// endpoint, not just the latest line shown in the task's progress.
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				tm.logBuildLine(task.ID, line)
				pct := estimateBuildProgress(line)
				if pct < 0 {
					pct = -1 // show indeterminate
				}
				task.UpdateProgress(TaskRunning, line, pct)
			}
		}()

		// Read stderr for build diagnostics.
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				line := scanner.Text()
				tm.logBuildLine(task.ID, line)
				if pct := estimateBuildProgress(line); pct >= 0 {
					task.UpdateProgress(TaskRunning, line, pct)
				} else if strings.Contains(line, "error") || strings.Contains(line, "Error") {
					task.UpdateProgress(TaskRunning, line, -1)
				}
			}
		}()

		// Wait for build to finish or cancellation
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		select {
		case <-task.Done():
			cmd.Process.Kill()
			os.RemoveAll(stagingDir)
			return
		case err := <-waitDone:
			if err != nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Build failed: %v", err), 0)
				os.RemoveAll(stagingDir)
				return
			}
		}

		// List built binaries
		entries, _ := os.ReadDir(stagingDir)
		var bins []string
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "meta.json" {
				bins = append(bins, e.Name())
			}
		}
		if len(bins) == 0 {
			task.UpdateProgress(TaskFailed, "Build completed but no binaries found", 0)
			os.RemoveAll(stagingDir)
			return
		}

		if replaceExisting {
			if err := os.RemoveAll(finalDir); err != nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Build succeeded but failed to remove old backend: %v", err), 0)
				return
			}
		}
		if err := os.Rename(stagingDir, finalDir); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Build succeeded but failed to install it: %v", err), 0)
			return
		}

		task.UpdateProgress(TaskCompleted,
			fmt.Sprintf("Build complete: %s", strings.Join(bins, ", ")), 100)
	}()

	return task
}

// estimateBuildProgress tries to guess build progress from output lines.
func estimateBuildProgress(line string) int {
	// Docker step markers like [1/12]
	if len(line) > 3 && line[0] == '[' && line[1] >= '0' && line[1] <= '9' {
		var step, total int
		if n, _ := fmt.Sscanf(line, "[%d/%d", &step, &total); n == 2 && total > 0 {
			return step * 100 / total
		}
	}
	// CMake percentage like [42%]
	var pct int
	if n, _ := fmt.Sscanf(line, "[%d%%]", &pct); n == 1 && pct >= 0 && pct <= 100 {
		return pct
	}
	return -1
}
