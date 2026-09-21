// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

// TestRunVersion verifica que --version imprime nombre + versión + paridad a
// stdout (no a stderr) y sale con 0.
func TestRunVersion(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"--version"}, &out, &errBuf)

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr no vacío: %q", errBuf.String())
	}
	got := out.String()
	for _, want := range []string{"kiro-gateway " + version.Version(), "parity:", version.Upstream, version.UpstreamCommit} {
		if !strings.Contains(got, want) {
			t.Errorf("--version output missing %q; got: %q", want, got)
		}
	}
}

// TestRunHelp verifica que --help imprime la ayuda moderna a stdout con 0.
func TestRunHelp(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"--help"}, &out, &errBuf)

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	got := out.String()
	for _, want := range []string{"Usage:", "Flags:", "Examples:", "--host", "--health"} {
		if !strings.Contains(got, want) {
			t.Errorf("--help output missing %q; got: %q", want, got)
		}
	}
}

// TestRunUnknownFlag verifica que un flag desconocido sale con 2 y escribe el
// error a stderr (no a stdout).
func TestRunUnknownFlag(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"--definitely-not-a-flag"}, &out, &errBuf)

	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "not defined") {
		t.Errorf("stderr sin el error de flag; got: %q", errBuf.String())
	}
}

// TestNewLoggerLevels cubre el mapeo de LOG_LEVEL a nivel, incluido OFF→nil.
func TestNewLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	for _, level := range []string{"DEBUG", "INFO", "WARN", "ERROR", "DESCONOCIDO"} {
		if newLogger(level, &buf) == nil {
			t.Errorf("newLogger(%q) = nil, want logger", level)
		}
	}
	for _, off := range []string{"OFF", "NONE", "SILENT"} {
		if newLogger(off, &buf) != nil {
			t.Errorf("newLogger(%q) != nil, want nil (logging desactivado)", off)
		}
	}
}
