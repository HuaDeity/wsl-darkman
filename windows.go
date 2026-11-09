//go:build linux

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

//go:embed windows-watcher.ps1
var watcherScript string

func encodeScript(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func windowsCommand(o options, once bool) (*exec.Cmd, error) {
	executable := o.powershell
	if executable == "" {
		executable, _ = exec.LookPath("powershell.exe")
	}
	if executable == "" {
		fallback := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
		if info, err := os.Stat(fallback); err == nil && info.Mode().IsRegular() {
			executable = fallback
		}
	}
	if executable == "" {
		return nil, fmt.Errorf("powershell.exe not found; enable WSL interop or use --powershell PATH")
	}
	value := "AppsUseLightTheme"
	if o.source == "system" {
		value = "SystemUsesLightTheme"
	}
	onceValue := "$false"
	if once {
		onceValue = "$true"
	}
	script := strings.NewReplacer("__VALUE__", value, "__ONCE__", onceValue).Replace(watcherScript)
	cmd := exec.Command(executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeScript(script))
	cmd.Env = interopEnvironment(os.Environ(), "/run/WSL")
	return cmd, nil
}

func environmentValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], key+"="); ok {
			return value
		}
	}
	return ""
}

func setEnvironment(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func interopEnvironment(env []string, directory string) []string {
	if inherited := environmentValue(env, "WSL_INTEROP"); inherited != "" {
		if info, err := os.Stat(inherited); err == nil && info.Mode()&os.ModeSocket != 0 {
			return env
		}
	}
	paths, _ := filepath.Glob(filepath.Join(directory, "*_interop"))
	var newest os.FileInfo
	var selected string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			continue
		}
		if newest == nil || info.ModTime().After(newest.ModTime()) {
			newest, selected = info, path
		}
	}
	if selected != "" {
		return setEnvironment(env, "WSL_INTEROP", selected)
	}
	return env
}

func readMode(ctx context.Context, o options) (mode, error) {
	cmd, err := windowsCommand(o, true)
	if err != nil {
		return "", err
	}
	queryCtx, cancel := context.WithTimeout(ctx, o.startupTimeout)
	defer cancel()
	query := commandWithCancellation(queryCtx, cmd.Args)
	query.Env = cmd.Env
	var stdout, stderr bytes.Buffer
	query.Stdout, query.Stderr = &stdout, &stderr
	if err := query.Run(); err != nil {
		return "", fmt.Errorf("query Windows mode: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseMode(stdout.String())
}
