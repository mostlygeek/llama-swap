package mantle

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartBuild kicks off a container-local build of a llama.cpp fork.
// Runs as a goroutine, emitting progress events via the task.
func (tm *TaskManager) StartBuild(repo, branch, buildScript, backendsDir string, cmakeArgs []string) *Task {
	task := tm.CreateTask("build", repo, branch, "")

	go func() {
		task.UpdateProgress(TaskRunning, fmt.Sprintf("Building %s@%s...", repo, branch), 0)

		if branch == "" {
			branch = "main"
		}
		outDir := filepath.Join(backendsDir, "build-"+task.ID)
		if err := os.MkdirAll(outDir, 0755); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to create output dir: %v", err), 0)
			return
		}

		args := []string{
			buildScript,
			"--repo", repo,
			"--branch", branch,
			"--out", outDir,
		}
		for _, arg := range cmakeArgs {
			args = append(args, "--cmake-arg", arg)
		}
		cmd := exec.Command("bash", args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to get stdout: %v", err), 0)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to get stderr: %v", err), 0)
			return
		}

		if err := cmd.Start(); err != nil {
			task.UpdateProgress(TaskFailed, fmt.Sprintf("Failed to start build: %v", err), 0)
			return
		}

		// Read stdout for progress
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
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
			os.RemoveAll(outDir)
			return
		case err := <-waitDone:
			if err != nil {
				task.UpdateProgress(TaskFailed, fmt.Sprintf("Build failed: %v", err), 0)
				return
			}
		}

		// List built binaries
		entries, _ := os.ReadDir(outDir)
		var bins []string
		for _, e := range entries {
			if !e.IsDir() {
				bins = append(bins, e.Name())
			}
		}
		if len(bins) == 0 {
			task.UpdateProgress(TaskFailed, "Build completed but no binaries found", 0)
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
