//go:build !windows

package main

import "testing"

func testTerminalLifetime(t *testing.T, executable string) { t.Helper() }

func verifyStartup(t *testing.T, executable string) { t.Helper() }

func restoreStartup(t *testing.T)                                     { t.Helper() }
func verifyShortcut(t *testing.T, path, executable, arguments string) { t.Helper() }
func testTrayExecutable(t *testing.T, executable string)              { t.Helper() }
