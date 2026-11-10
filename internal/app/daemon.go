//go:build linux

package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type watcherEvent struct {
	mode mode
	err  error
}

func followWatcher(ctx context.Context, cmd *exec.Cmd, o options, apply func(context.Context, mode) bool) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return err
	}
	readerCtx, cancelReader := context.WithCancel(ctx)
	events := make(chan watcherEvent, 16)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 256), 4096)
		for scanner.Scan() {
			m, err := parseMode(scanner.Text())
			select {
			case events <- watcherEvent{m, err}:
			case <-readerCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case events <- watcherEvent{err: err}:
		case <-readerCtx.Done():
		}
	}()
	defer func() {
		cancelReader()
		// EOF is the cooperative shutdown signal to the Windows watcher.
		stdin.Close()
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		timer := time.NewTimer(o.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-waited:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-waited
		}
		stdout.Close()
		<-readerDone
	}()
	startup := time.NewTimer(o.startupTimeout)
	defer startup.Stop()
	startupDeadline := startup.C
	var previous mode
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-startupDeadline:
			return fmt.Errorf("Windows watcher did not report its initial mode within %s", o.startupTimeout)
		case event := <-events:
			if event.err != nil {
				return fmt.Errorf("Windows watcher: %w", event.err)
			}
			if previous == "" {
				startup.Stop()
				startupDeadline = nil
			}
			if event.mode != previous {
				apply(ctx, event.mode)
				if ctx.Err() != nil {
					return nil
				}
				previous = event.mode
			}
		}
	}
}

func acquireLock() (*os.File, error) {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		root = xdgPath("XDG_CACHE_HOME", ".cache")
	}
	directory := filepath.Join(root, "wsl-darkman")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(directory, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("another wsl-darkman daemon is already running")
		}
		return nil, err
	}
	return lock, nil
}

func reconnect(ctx context.Context, o options, session func(context.Context) error) {
	delay := o.retryMin
	for ctx.Err() == nil {
		started := time.Now()
		err := session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= time.Minute {
			delay = o.retryMin
		}
		log.Printf("%v; reconnecting in %s", err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay *= 2
		if delay > o.retryMax {
			delay = o.retryMax
		}
	}
}

func runDaemon(ctx context.Context, o options) error {
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var portal *settingsPortal
	if !o.noPortal {
		portal, err = startSettingsPortal(ctx, sessionBusAddress())
		if err != nil {
			return fmt.Errorf("start appearance portal: %w (use --no-portal for preferences and hooks only)", err)
		}
		defer portal.conn.Close()
		stopDisconnect := context.AfterFunc(portal.conn.Context(), cancel)
		defer stopDisconnect()
		log.Print("Serving the XDG appearance settings portal")
	}
	reconnect(runCtx, o, func(ctx context.Context) error {
		cmd, err := windowsCommand(o, false)
		if err != nil {
			return err
		}
		return followWatcher(ctx, cmd, o, func(ctx context.Context, m mode) bool {
			if portal != nil {
				if err := portal.update(m); err != nil {
					log.Printf("Publish appearance: %v", err)
					cancel()
					return false
				}
			}
			return applyMode(ctx, m, o)
		})
	})
	if ctx.Err() == nil && runCtx.Err() != nil {
		return fmt.Errorf("appearance portal disconnected; restart the service to reconnect")
	}
	return nil
}
