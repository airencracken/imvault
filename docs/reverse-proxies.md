# HTTPS reverse proxies

**Caddy is the recommended setup**, with automatic certificate issuance and
renewal. nginx and Apache 2.4 are supported alternatives. Each serves imvault
at the root of its own hostname, such as `https://img.example.com`; a URL
prefix such as `/imvault/` is not supported by these examples.

## Configure imvault

For a native service and proxy on the same host, set:

```sh
IMVAULT_ADDR="127.0.0.1:8080"
IMVAULT_BASE_URL="https://img.example.com"
IMVAULT_SECURE_COOKIES="true"
IMVAULT_TRUST_PROXY_HEADERS="true"
```

Put these in `/etc/conf.d/imvault` for OpenRC or `/etc/imvault/imvault.env` for
systemd, then restart imvault. Keep port 8080 private. The examples replace
incoming forwarding headers with the actual client address and HTTPS scheme;
imvault uses those headers for rate limits and secure links.

These examples assume the proxy directly accepts visitors' connections.
If you add a CDN or another proxy, configure the trusted upstream addresses
in the web server before accepting its client-IP headers. For containers,
use a private network and the application's service name instead of host
loopback; do not expose the application's port to the internet.

## Generate a site configuration

The binary includes all three templates, so a package install has the helpers
available without finding example files:

```sh
imvault --help
imvault proxy-config caddy --domain img.example.com > imvault.Caddyfile
imvault proxy-config nginx --domain img.example.com > imvault.nginx.conf
imvault proxy-config apache --domain img.example.com > imvault.apache.conf
```

Use `--upstream HOST:PORT` to change the loopback destination. It defaults to
the native service port. nginx and Apache accept `--tls-cert /path/fullchain.pem`
and `--tls-key /path/privkey.pem` together; otherwise their paths follow Certbot's
`/etc/letsencrypt/live/HOST/` layout. Caddy manages certificates automatically.

These commands print config to stdout. They do not change the service, obtain
certificates, or reload the proxy. Copy the application settings from the
generated comments into your service configuration, then include the site
in the proxy's configuration and validate it with the commands below.
The helpers support a proxy and application on the same host; use the static
examples as a starting point for other network arrangements.

## Caddy: recommended

Use [`contrib/caddy/Caddyfile`](../contrib/caddy/Caddyfile) and the
[Caddy deployment guide](deployment.md#tls-with-caddy). It handles TLS,
streams uploads and exports, and leaves application response headers intact.
Point DNS at the host and allow public ports 80 and 443.

Validate the complete configuration before reloading:

```sh
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

## nginx

Start with [`contrib/nginx/imvault.conf`](../contrib/nginx/imvault.conf).
Include it from nginx's `http` block, replace `img.example.com`, and set
the certificate/key paths to certificates you already manage. The example
paths follow Certbot's layout; nginx does not obtain or renew them for you.
Arrange automatic renewal and a successful configuration check/reload afterward.

The example redirects HTTP to HTTPS, preserves the host and application
headers, replaces client-IP headers, and disables request/response buffering
and proxy caching. This permits streaming uploads, exports, and media ranges.
Its **256 MiB limit applies to the whole request**, including every file and
multipart overhead. Adjust it to fit your batch sizes and imvault limits.
The 300-second proxy timeouts allow quiet intervals during media processing;
they do not change the application's own limits.

Run these administrative commands after including the site:

```sh
nginx -t
# Only reload after the check succeeds.
nginx -s reload
```

Keep the example's `X-Forwarded-For $remote_addr` assignment. Appending an
untrusted incoming value with `$proxy_add_x_forwarded_for` would allow a
visitor to choose the first address that imvault trusts. See nginx's
[proxy header and buffering reference](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

## Apache 2.4

Start with [`contrib/apache/imvault.conf`](../contrib/apache/imvault.conf).
Load `mod_ssl`, `mod_proxy`, `mod_proxy_http`, `mod_headers`, and `mod_alias`.
On Debian/Ubuntu, `a2enmod ssl proxy proxy_http headers alias` enables them;
on Gentoo, enable those modules in your Apache package/server configuration.
The main configuration must listen on ports 80 and 443. Include the virtual
host file once, replace the hostname and certificate paths, and arrange
certificate renewal with a configuration check/reload afterward.

The HTTPS virtual host disables forward proxying, preserves the host, replaces
forwarded headers, and passes all routes through to imvault. Do not put a
cache or a static-file alias in front of private media routes. The whole-request
body limit is **256 MiB**; increase it for larger batches as needed. Range
requests, application cookies, and streaming responses pass through the proxy.

Validate and gracefully reload as an administrator:

```sh
apachectl configtest
# Only reload after the check succeeds.
apachectl graceful
```

Some distributions name the command `apache2ctl`. Preserve `ProxyAddHeaders Off`
and the explicit `RequestHeader` assignments together; Apache's default is
to append forwarded headers. See the
[Apache proxy reference](https://httpd.apache.org/docs/2.4/mod/mod_proxy.html#proxyaddheaders).

## Verify your deployment

Check `/healthz` through the public HTTPS hostname. Sign in, upload a photo or
clip, and confirm private media stays private. Test clip seeking and an export
if you use those features. Do not add a strict Content-Security-Policy without
reading [Security](security.md); the application supplies its own headers.

`make test-proxies` runs the nginx and Apache examples against a temporary
HTTP backend over local TLS. It checks spoofed headers, cookies, authentication
and HTMX headers, escaped URLs, uploads over 1 MiB, ranges, access-denied
responses, and streaming. It requires both servers and fails if either is
missing. Set `NGINX_BINARY`, `APACHE_BINARY`, and `APACHE_MODULE_DIR` for custom
installations; the default Apache module path is `/usr/lib/apache2/modules`.
CI runs these checks before building release packages.
