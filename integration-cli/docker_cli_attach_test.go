package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/integration-cli/cli"
	"github.com/go-check/check"
	"gotest.tools/assert"
	"gotest.tools/icmd"
)

const attachWait = 5 * time.Second

func (s *DockerSuite) TestAttachMultipleAndRestart(c *testing.T) {
	endGroup := &sync.WaitGroup{}
	startGroup := &sync.WaitGroup{}
	endGroup.Add(3)
	startGroup.Add(3)

	cli.DockerCmd(c, "run", "--name", "attacher", "-d", "busybox", "/bin/sh", "-c", "while true; do sleep 1; echo hello; done")
	cli.WaitRun(c, "attacher")

	startDone := make(chan struct{})
	endDone := make(chan struct{})

	go func() {
		endGroup.Wait()
		close(endDone)
	}()

	go func() {
		startGroup.Wait()
		close(startDone)
	}()

	for i := 0; i < 3; i++ {
		go func() {
			cmd := exec.Command(dockerBinary, "attach", "attacher")

			defer func() {
				cmd.Wait()
				endGroup.Done()
			}()

			out, err := cmd.StdoutPipe()
			if err != nil {
				c.Fatal(err)
			}
			defer out.Close()

			if err := cmd.Start(); err != nil {
				c.Fatal(err)
			}

			buf := make([]byte, 1024)

			if _, err := out.Read(buf); err != nil && err != io.EOF {
				c.Fatal(err)
			}

			startGroup.Done()

			if !strings.Contains(string(buf), "hello") {
				c.Fatalf("unexpected output %s expected hello\n", string(buf))
			}
		}()
	}

	select {
	case <-startDone:
	case <-time.After(attachWait):
		c.Fatalf("Attaches did not initialize properly")
	}

	cli.DockerCmd(c, "kill", "attacher")

	select {
	case <-endDone:
	case <-time.After(attachWait):
		c.Fatalf("Attaches did not finish properly")
	}
}

func (s *DockerSuite) TestAttachTTYWithoutStdin(c *testing.T) {
	// TODO @jhowardmsft. Figure out how to get this running again reliable on Windows.
	// It works by accident at the moment. Sometimes. I've gone back to v1.13.0 and see the same.
	// On Windows, docker run -d -ti busybox causes the container to exit immediately.
	// Obviously a year back when I updated the test, that was not the case. However,
	// with this, and the test racing with the tear-down which panic's, sometimes CI
	// will just fail and `MISS` all the other tests. For now, disabling it. Will
	// open an issue to track re-enabling this and root-causing the problem.
	testRequires(c, DaemonIsLinux)
	out, _ := dockerCmd(c, "run", "-d", "-ti", "busybox")

	id := strings.TrimSpace(out)
	assert.NilError(c, waitRun(id))

	done := make(chan error)
	go func() {
		defer close(done)

		cmd := exec.Command(dockerBinary, "attach", id)
		if _, err := cmd.StdinPipe(); err != nil {
			done <- err
			return
		}

		expected := "the input device is not a TTY"
		if runtime.GOOS == "windows" {
			expected += ".  If you are using mintty, try prefixing the command with 'winpty'"
		}
		if out, _, err := runCommandWithOutput(cmd); err == nil {
			done <- fmt.Errorf("attach should have failed")
			return
		} else if !strings.Contains(out, expected) {
			done <- fmt.Errorf("attach failed with error %q: expected %q", out, expected)
			return
		}
	}()

	select {
	case err := <-done:
		assert.NilError(c, err)
	case <-time.After(attachWait):
		c.Fatal("attach is running but should have failed")
	}
}

func (s *DockerSuite) TestAttachDisconnect(c *testing.T) {
	testRequires(c, DaemonIsLinux)
	out, _ := dockerCmd(c, "run", "-di", "busybox", "/bin/cat")
	id := strings.TrimSpace(out)

	cmd := exec.Command(dockerBinary, "attach", id)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	assert.NilError(c, err)
	defer stdout.Close()
	assert.Assert(c, cmd.Start() == nil)
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	_, err = stdin.Write([]byte("hello\n"))
	assert.NilError(c, err)
	out, err = bufio.NewReader(stdout).ReadString('\n')
	assert.NilError(c, err)
	assert.Equal(c, strings.TrimSpace(out), "hello")

	assert.Assert(c, stdin.Close() == nil)

	// Expect container to still be running after stdin is closed
	running := inspectField(c, id, "State.Running")
	assert.Equal(c, running, "true")
}

func (s *DockerSuite) TestAttachPausedContainer(c *testing.T) {
	testRequires(c, IsPausable)
	runSleepingContainer(c, "-d", "--name=test")
	dockerCmd(c, "pause", "test")

	result := dockerCmdWithResult("attach", "test")
	result.Assert(c, icmd.Expected{
		Error:    "exit status 1",
		ExitCode: 1,
		Err:      "You cannot attach to a paused container, unpause it first",
	})
}
