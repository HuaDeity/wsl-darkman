//go:build linux

package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func xdgPath(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func hooksFor(m mode) [][]string {
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	var roots []string
	for _, root := range filepath.SplitList(dataDirs) {
		if root != "" {
			roots = append(roots, root)
		}
	}
	roots = append(roots, xdgPath("XDG_DATA_HOME", ".local/share"))
	found := map[string][]string{}
	// Unified hooks override legacy hooks across roots. Within either pass,
	// later system roots override earlier ones, then user entries override them.
	for _, directory := range []string{string(m) + "-mode.d", "darkman"} {
		for _, root := range roots {
			path := filepath.Join(root, directory)
			entries, err := os.ReadDir(path)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Printf("Cannot read hooks in %s: %v", path, err)
				}
				continue
			}
			for _, entry := range entries {
				hook := filepath.Join(path, entry.Name())
				info, err := os.Stat(hook)
				if err != nil || !info.Mode().IsRegular() || syscall.Access(hook, 1) != nil {
					delete(found, entry.Name())
					continue
				}
				command := []string{hook}
				if directory == "darkman" {
					command = append(command, string(m))
				}
				found[entry.Name()] = command
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	commands := make([][]string, 0, len(names))
	for _, name := range names {
		commands = append(commands, found[name])
	}
	return commands
}

// commandWithCancellation kills the full Linux process group on cancellation,
// including hook descendants which would survive killing only the direct child.
func commandWithCancellation(ctx context.Context, arguments []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == syscall.ESRCH {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	cmd.WaitDelay = time.Second
	return cmd
}

func runAction(ctx context.Context, command []string, timeout time.Duration, env []string) bool {
	if ctx.Err() != nil {
		return false
	}
	actionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := commandWithCancellation(actionCtx, command)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Action failed (%s): %v", strings.Join(command, " "), err)
		return false
	}
	return true
}

func applyMode(ctx context.Context, m mode, o options) bool {
	log.Printf("Applying %s mode", m)
	if o.backend == "darkman" {
		return runAction(ctx, []string{"darkman", "set", string(m)}, o.hookTimeout, nil)
	}
	ok := true
	if !o.noGSettings {
		if _, err := exec.LookPath("gsettings"); err == nil {
			ok = runAction(ctx, []string{"gsettings", "set", "org.gnome.desktop.interface", "color-scheme", "prefer-" + string(m)}, o.hookTimeout, nil)
		}
	}
	env := setEnvironment(os.Environ(), "WSL_DARKMAN_MODE", string(m))
	for _, command := range hooksFor(m) {
		if ctx.Err() != nil {
			return false
		}
		if !runAction(ctx, command, o.hookTimeout, env) {
			ok = false
		}
	}
	return ok
}
