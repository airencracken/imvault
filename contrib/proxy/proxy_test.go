//go:build proxyintegration

// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy_test

import (
	"testing"

	"github.com/airencracken/comfylib/proxyconfig/proxytest"

	"imvault/contrib"
)

// TestReverseProxies runs the generated nginx and Apache configurations in real
// servers; `make test-proxies` needs both installed.
func TestReverseProxies(t *testing.T) {
	proxytest.ReverseProxies(t, contrib.ProxySpec)
}
