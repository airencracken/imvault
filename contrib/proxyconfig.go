// SPDX-License-Identifier: AGPL-3.0-or-later

// Package contrib embeds the reverse-proxy examples shipped with the packages,
// so that `imvault proxy-config` prints exactly what they install.
package contrib

import (
	"embed"

	"github.com/airencracken/comfylib/proxyconfig"
)

//go:embed caddy/Caddyfile nginx/imvault.conf apache/imvault.conf
var examples embed.FS

// ProxySpec describes Imvault's examples to comfylib's proxyconfig: the
// hostname and loopback upstream written in them, and the setting that makes
// Imvault believe the proxy's forwarding headers.
var ProxySpec = proxyconfig.Spec{
	App:              "imvault",
	ExampleDomain:    "img.example.com",
	DefaultUpstream:  "127.0.0.1:8080",
	ProxyEnvironment: "IMVAULT_TRUSTED_PROXIES=127.0.0.1/32,::1/128",
	Examples:         examples,
}
