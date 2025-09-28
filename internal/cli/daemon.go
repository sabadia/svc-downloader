package cli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sabadia/svc-downloader/internal/config/sys"
)

func startDaemon(o *Options) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.PIDFile), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(o.PIDFile); err == nil {
		pid, _ := readPID(o.PIDFile)
		if pid > 0 && processAlive(pid) {

			return errors.New("already running")
		}
		_ = removePIDFile(o.PIDFile)
	}

	o.Daemonize = true

	args := buildArgsFromOptions(o)
	cmd := exec.Command(execPath, args...)

	sys.SetSysProcAttr(cmd)
	if err := os.MkdirAll(filepath.Dir(o.LogFile), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(o.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	// Write PID file
	if err := writePIDFile(o.PIDFile, cmd.Process.Pid); err != nil {
		err := cmd.Process.Kill()
		if err != nil {
			return errors.New("failed to write pid file and failed to kill process: " + err.Error())
		}
	}

	return nil
}

func stopDaemon(o *Options, gracefulSecs int) error {
	pid, err := readPID(o.PIDFile)
	if err != nil || pid <= 0 {
		return errors.New("not running")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	// Send SIGTERM
	_ = proc.Signal(syscall.SIGTERM)
	// Wait up to gracefulSecs for exit
	deadline := time.Now().Add(time.Duration(gracefulSecs) * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = removePIDFile(o.PIDFile)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Force kill
	_ = proc.Kill()
	_ = removePIDFile(o.PIDFile)
	return nil
}

func writePIDFile(pidFile string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644)
}

func readPID(pidFile string) (int, error) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func removePIDFile(pidFile string) error {
	if err := os.Remove(pidFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On macOS, sending signal 0 will return error if process does not exist
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return !errors.Is(err, os.ErrProcessDone)
	}
	return true
}
