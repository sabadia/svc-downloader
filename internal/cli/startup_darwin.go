//go:build darwin

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.sabadia.svcdownloader.plist")
}

func enableStartup(o *Options) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(o.LogFile), 0o755)
	plist := buildLaunchdPlist(execPath, buildArgsFromOptions(o), o)
	path := launchAgentPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	// load & enable
	cmd := exec.Command("launchctl", "load", "-w", path)
	return cmd.Run()
}

func disableStartup(o *Options) error {
	path := launchAgentPath()
	_ = exec.Command("launchctl", "unload", "-w", path).Run()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func buildLaunchdPlist(execPath string, args []string, o *Options) string {
	// Launchd will run our binary in foreground; logs go to specified paths
	// ProgramArguments must be a full list including binary path
	progArgs := append([]string{execPath}, args...)
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>com.sabadia.svcdownloader</string>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	b.WriteString("\t<key>WorkingDirectory</key>\n\t<string>" + xmlEscape(o.DataDir) + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range progArgs {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>StandardOutPath</key>\n\t<string>" + xmlEscape(o.LogFile) + "</string>\n")
	b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" + xmlEscape(o.LogFile) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}
