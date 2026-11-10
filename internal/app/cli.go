//go:build linux

// Package app implements the wsl-darkman CLI and appearance synchronization.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type mode string

const (
	light mode = "light"
	dark  mode = "dark"
)

type options struct {
	command, source, powershell     string
	gtkLightTheme, gtkDarkTheme     string
	noGSettings, noPortal           bool
	hookTimeout                     time.Duration
	startupTimeout, shutdownTimeout time.Duration
	retryMin, retryMax              time.Duration
}

func defaultOptions() options {
	return options{
		command: "run", source: "apps",
		hookTimeout: 10 * time.Second, startupTimeout: 30 * time.Second,
		shutdownTimeout: 5 * time.Second, retryMin: time.Second, retryMax: 30 * time.Second,
	}
}

func parseOptions(args []string, output io.Writer) (options, error) {
	o := defaultOptions()
	f := flag.NewFlagSet("wsl-darkman", flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&o.source, "source", o.source, "Windows appearance: apps or system")
	f.StringVar(&o.powershell, "powershell", "", "Windows powershell.exe path inside WSL")
	f.BoolVar(&o.noGSettings, "no-gsettings", false, "Skip the standalone GNOME preference update")
	f.BoolVar(&o.noPortal, "no-portal", false, "Disable the XDG appearance settings portal")
	f.StringVar(&o.gtkLightTheme, "gtk-light-theme", "", "Installed GTK theme for light mode (requires --gtk-dark-theme)")
	f.StringVar(&o.gtkDarkTheme, "gtk-dark-theme", "", "Installed GTK theme for dark mode (requires --gtk-light-theme)")
	seconds := f.Float64("hook-timeout", 10, "Seconds allowed per action")
	f.Usage = func() {
		fmt.Fprintln(output, "Usage: wsl-darkman [run|get|once] [options]")
		fmt.Fprintln(output, "Follow Windows appearance from WSL. Default command: run.")
		f.PrintDefaults()
	}
	// flag normally stops at a positional argument. Remove the command first,
	// retaining values belonging to options, to support both CLI orderings.
	var flags []string
	commandSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") {
				if entry := f.Lookup(name); entry != nil {
					boolean, ok := entry.Value.(interface{ IsBoolFlag() bool })
					if !(ok && boolean.IsBoolFlag()) && i+1 < len(args) {
						i++
						flags = append(flags, args[i])
					}
				}
			}
		} else if !commandSeen && (arg == "run" || arg == "get" || arg == "once") {
			o.command, commandSeen = arg, true
		} else {
			return o, fmt.Errorf("unexpected argument %q (expected run, get, or once)", arg)
		}
	}
	if err := f.Parse(flags); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if o.source != "apps" && o.source != "system" {
		return o, fmt.Errorf("source must be apps or system")
	}
	if (o.gtkLightTheme == "") != (o.gtkDarkTheme == "") {
		return o, fmt.Errorf("gtk-light-theme and gtk-dark-theme must be supplied together")
	}
	if o.noGSettings && o.gtkLightTheme != "" {
		return o, fmt.Errorf("GTK theme options cannot be combined with no-gsettings")
	}
	if math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds <= 0 ||
		*seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return o, fmt.Errorf("hook-timeout must be a finite positive duration")
	}
	o.hookTimeout = time.Duration(*seconds * float64(time.Second))
	if o.hookTimeout <= 0 {
		return o, fmt.Errorf("hook-timeout must be at least one nanosecond")
	}
	return o, nil
}

func parseMode(raw string) (mode, error) {
	switch m := mode(strings.TrimSpace(raw)); m {
	case dark, light:
		return m, nil
	default:
		return "", fmt.Errorf("unexpected Windows watcher output: %.100q", raw)
	}
}

func execute(ctx context.Context, o options, output io.Writer) error {
	if o.command == "run" {
		return runDaemon(ctx, o)
	}
	m, err := readMode(ctx, o)
	if err != nil {
		return err
	}
	if o.command == "get" {
		_, err = fmt.Fprintln(output, m)
		return err
	}
	if !applyMode(ctx, m, o) {
		return errors.New("one or more appearance actions failed")
	}
	return nil
}

// Run executes the CLI and returns its process exit status. It owns signal
// handling so the command entry point only needs to pass arguments and streams.
func Run(args []string, stdout, stderr io.Writer) int {
	log.SetPrefix("wsl-darkman: ")
	log.SetFlags(0)
	o, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "wsl-darkman: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, o, stdout); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "wsl-darkman: %v\n", err)
		return 1
	}
	return 0
}
