package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

/*
**
Test how exec.Cmd.CommandContext behaves under certain conditions:*

  - process is killed externally, what happens with cmd.Wait() *
    ✔︎ it returns. catches crashes.*

  - process ignores SIGTERM*
    ✔︎ `kill()` is called after cmd.WaitDelay*

  - this process exits, what happens with children (kill -9 <this process' pid>)*
    x they stick around. have to be manually killed.*

  - .WithTimeout()'s cancel is called *
    ✔︎ process is killed after it ignores sigterm, cmd.Wait() catches it.*

  - parent receives SIGINT/SIGTERM, what happens
    ✔︎ waits for child process to exit, then exits gracefully.
*/
func main() {

	// swap between these to use kill -9 <pid> on the cli to sim external crash
	ctx, cancel := context.WithCancel(context.Background())
	//ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	//cmd := exec.CommandContext(ctx, "sleep", "1")
	cmd := exec.CommandContext(ctx,
		"../../build/simple-responder_darwin_arm64",
		//"-ignore-sig-term", /* so it doesn't exit on receiving SIGTERM, test cmd.WaitTimeout */
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// set a wait delay before signing sig kill
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Cancel = func() error {
		fmt.Println("✔︎ Cancel() called, sending SIGTERM")
		cmd.Process.Signal(syscall.SIGTERM)

		//return nil

		// this error is returned by cmd.Wait(), and can be used to
		// single an error when the process couldn't be normally terminated
		// but since a SIGTERM is sent, it's probably ok to return a nil
		// as WaitDelay timing out will override the any error set here.
		//
		// test by enabling/disabling -ignore-sig-term on the process
		// with -ignore-sig-term enabled, cmd.Wait() will have "signal: killed"
		// without it, it will show the "new error from cancel"
		return errors.New("error from cmd.Cancel()") // sets error returned by cmd.Wait()
	}

	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting process:", err)
		return
	}

	// catch signals. Calls cancel() which will cause cmd.Wait() to return and
	// this program to eventually exit gracefully.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		signal := <-sigChan
		fmt.Printf("✔︎ Received signal: %d, Killing process... with cancel before exiting\n", signal)
		cancel()
	}()

	fmt.Printf("✔︎ Parent Pid: %d, Process Pid: %d\n", os.Getpid(), cmd.Process.Pid)
	fmt.Println("✔︎ Process started, cmd.Wait() ... ")
	if err := cmd.Wait(); err != nil {
		fmt.Println("✔︎ cmd.Wait returned, Error:", err)
	} else {
		fmt.Println("✔︎ cmd.Wait returned, Process exited on its own")
	}
	fmt.Println("✔︎ Child process exited, Done.")
}
