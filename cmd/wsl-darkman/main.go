//go:build linux

package main

import (
	"os"

	"github.com/HuaDeity/wsl-darkman/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
