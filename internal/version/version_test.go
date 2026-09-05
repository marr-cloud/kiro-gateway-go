// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package version

import "testing"

func TestVersionDefault(t *testing.T) {
	if got := Version(); got != "2.4.dev.13+go" {
		t.Errorf("Version() = %q, quiero %q", got, "2.4.dev.13+go")
	}
}

func TestUpstreamMetadata(t *testing.T) {
	if Upstream != "2.4.dev.13" {
		t.Errorf("Upstream = %q, quiero %q", Upstream, "2.4.dev.13")
	}
	if UpstreamCommit != "a5292ca" {
		t.Errorf("UpstreamCommit = %q, quiero %q", UpstreamCommit, "a5292ca")
	}
}
