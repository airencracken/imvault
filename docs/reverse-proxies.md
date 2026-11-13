# HTTPS reverse proxies

Caddy is the recommended proxy because it obtains and renews certificates
itself. nginx and Apache 2.4 work too. Each example serves Imvault at the root
of its own hostname, such as `https://img.example.com`; URL prefixes such as
`/imvault/` are not supported.

## Configure Imvault

With a native service and the proxy on the same host, set:

```sh
IMVAULT_ADDR="127.0.0.1:8080"
IMVAULT_BASE_URL="https://img.example.com"
IMVAULT_SECURE_COOKIES="true"
IMVAULT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
```

Put these in `/etc/conf.d/imvault` (OpenRC) or `/etc/imvault/imvault.env`
(systemd) and restart Imvault. Keep port 8080 private.

The examples overwrite incoming forwarding headers with the real client address
and scheme. Imvault trusts those headers only from addresses in
`IMVAULT_TRUSTED_PROXIES`, and uses them for rate limits and links. If a CDN or
another proxy sits in front, configure its addresses in the web server before
trusting its client-IP headers. With containers, use a private network and the
app's service name, list the proxy's address on that network in
`IMVAULT_TRUSTED_PROXIES`, and keep the app's port off the internet.

## Generate a site configuration

The binary carries all three templates:

```sh
imvault proxy-config caddy --domain img.example.com > imvault.Caddyfile
imvault proxy-config nginx --domain img.example.com > imvault.nginx.conf
imvault proxy-config apache --domain img.example.com > imvault.apache.conf
```

`--upstream HOST:PORT` changes the destination, which defaults to
`127.0.0.1:8080`. For nginx and Apache, pass `--tls-cert /path/fullchain.pem`
and `--tls-key /path/privkey.pem` together; otherwise the paths follow
Certbot's `/etc/letsencrypt/live/HOST/` layout.

The command only prints a configuration. It does not touch the service, fetch
certificates or reload the proxy. Copy the application settings from the
generated comments into your service configuration, include the site in the
proxy, and validate it as shown below. The generator assumes the proxy and app
share a host; for anything else, start from the files in `contrib/`.

## Caddy

Use [`contrib/caddy/Caddyfile`](../contrib/caddy/Caddyfile) and the
[Caddy deployment guide](deployment.md#tls-with-caddy). Point DNS at the host
and open ports 80 and 443. Validate before reloading:

```sh
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

## nginx

Include [`contrib/nginx/imvault.conf`](../contrib/nginx/imvault.conf) from
nginx's `http` block. Replace `img.example.com` and point the certificate and
key paths at certificates you manage. nginx does not renew them, so arrange
renewal followed by a check and reload.

The example redirects HTTP to HTTPS and turns off buffering and caching so
uploads, exports and media ranges stream. Its **256 MiB limit covers the whole
request**, every file included; raise it to fit your batch sizes. The 300-second
timeouts cover quiet stretches during media processing.

```sh
nginx -t
# Reload only after the check succeeds.
nginx -s reload
```

Keep `X-Forwarded-For $remote_addr`. `$proxy_add_x_forwarded_for` would pass
along whatever the visitor claimed. See nginx's
[proxy module reference](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

## Apache 2.4

Start with [`contrib/apache/imvault.conf`](../contrib/apache/imvault.conf).
Enable `mod_ssl`, `mod_proxy`, `mod_proxy_http`, `mod_headers` and `mod_alias`
(`a2enmod ssl proxy proxy_http headers alias` on Debian and Ubuntu). Apache must
listen on ports 80 and 443. Include the virtual host once, replace the hostname
and certificate paths, and arrange renewal followed by a check and reload.

The request limit is **256 MiB** for the whole request. Do not put a cache or a
static-file alias in front of private media routes.

```sh
apachectl configtest
# Reload only after the check succeeds.
apachectl graceful
```

Some distributions call it `apache2ctl`. Keep `ProxyAddHeaders Off` and the
explicit `RequestHeader` lines together; by default Apache appends forwarded
headers. See the
[Apache proxy reference](https://httpd.apache.org/docs/2.4/mod/mod_proxy.html#proxyaddheaders).

## Check the result

Open `/healthz` through the public hostname. Sign in, upload a photo or clip,
and confirm private media stays private. Try clip seeking and an export if you
use them. Read [Security](security.md) before adding a Content-Security-Policy;
Imvault sends its own headers.

`make test-proxies` runs the nginx and Apache examples against a local backend.
It needs both servers installed. Set `NGINX_BINARY`, `APACHE_BINARY` and
`APACHE_MODULE_DIR` for custom installs; the module directory defaults to
`/usr/lib/apache2/modules`.
