# tproxy-server

`tproxy-server` is the hosted half of a proof-of-concept WEB proxy type for
Telegram. A Telegram app keeps its normal MTProxy framing and encryption, but sends
all of its proxy TCP connections through one app-owned WebView transport. The
WebView carries a multiplexed session over a server-selected same-origin HTTPS or
WebSocket carrier.
The relay separates the logical streams again and connects each one to a stock
official MTProxy on the server.

The design is not tied to one Telegram client or operating system. Any Telegram
app that can host a WebView and connect its MTProxy sockets to a small local adapter
can implement the client side. The current proof-of-concept work includes a
Telegram Desktop implementation, an experimental Android client described in
[`ANDROID.md`](ANDROID.md), and an iOS client plan in [`IOS.md`](IOS.md). All use
the same bridge page, carrier selection, shared frame format, and server deployment.

The configured hostname remains a regular HTTPS website. A capability derived from
the hostname and MTProxy secret selects a one-shot bridge page. Requests without
authentic relay credentials receive the public site.

## How it works

```text
Telegram app
  MTProto connections with the normal MTProxy transform
          |
          v
  local WEB proxy adapter
  one logical stream per app connection
          |
          v
  one WebView transport and authenticated relay session
  multiplexed frames in the selected HTTPS/WebSocket carrier
          |
          v
  tproxy-server -> one local TCP connection per stream -> official MTProxy
```

The app configures only a hostname and an MTProxy secret. It derives the bridge
capability locally and never exposes the raw secret to JavaScript. The WebView
opens the bridge, exchanges a short-lived bootstrap token for a relay session, and
runs the carrier mode selected by the matching server profile. `OPEN`, `DATA`,
`WINDOW`, and `CLOSE` frames multiplex every app connection through that session.
The relay treats DATA as opaque bytes: it cannot choose a Telegram destination or
decrypt the MTProxy stream.

“One WebView transport” means one logical carrier and relay session for the app,
not one HTTP request or backend connection. The profile may use the original
serialized HTTPS carrier, independent HTTPS request lanes per Telegram logical
session, one multiplexed WebSocket, or an independent WebSocket per logical
session.

See [`PROTOCOL.md`](PROTOCOL.md) for the normative wire contract and
[`PLAN.md`](PLAN.md) for the architecture, limits, implementation rationale, and
remaining proof-of-concept work. [`PUBLIC_SITE.md`](PUBLIC_SITE.md) defines the
operator-owned website extension points.

The reference deployment layout is:

```text
Internet :80/:443 -> Caddy -> 127.0.0.1:8080 tproxy-server -> operator site
                                              |                (memory or loopback app)
                                              \-> 127.0.0.1:2398 official MTProxy
```

Only Caddy listens on external interfaces. The relay, its admin endpoints, the
official MTProxy client port, and MTProxy statistics remain local. The relay never
receives a client-selected backend address and never decrypts the MTProxy stream.

Caddy proxies **every** path to the relay. In static mode the relay serves the
whole site from memory using standard Go conditional and range handling. In application
mode it delegates ordinary and unauthenticated requests to one private loopback
web application. A request that proves knowledge of a bridge or session token is
intercepted before that application. In either mode there is no separately hosted
relay path for an unauthenticated prober to compare with the public site, and only
`GET /?bridge=<valid capability>` reveals the bridge. Restart the relay after
changing files under `public_dir`; the static site is read once at start-up.

## What you need

- a dedicated lowercase hostname such as `proxy.example.com` that you control;
- an **x86_64** Linux server with a public IPv4 address, SSH access, systemd, and
  either Ubuntu 22.04+ or Debian 12+;
- root or passwordless `sudo` on that server;
- public inbound TCP 80 and 443;
- one random 16-byte secret; and
- an operator-owned static site or a web application bound to a private loopback
  port.

The automated installer is intended for a clean server on which Caddy may own ports
80/443. It backs up an existing `/etc/caddy/Caddyfile`, but it then replaces the
active Caddy configuration. If the server already hosts other sites, use the manual
integration section instead.

## 1. Choose the hostname and secret

At your DNS provider, add an `A` record:

```text
proxy.example.com -> YOUR_SERVER_PUBLIC_IPV4
```

Add an `AAAA` record only when the server really has working public IPv6. Do not put
a CDN or HTTP proxy in front of this first deployment. Publish the hostname to
users in its ACE (`xn--…`) form if it is an internationalised name: the desktop
client stores and derives the capability from the A-label, ACE input round-trips
unchanged on every platform, while a hand-typed Unicode host containing `ß`, `ς`,
ZWJ/ZWNJ, or characters newer than Unicode 3.2 can be mapped differently by the
Qt 5.15 (Windows) and Qt 6 (macOS/Linux) desktop builds and then derive a
different capability. Wait until the record resolves from outside your network:

```bash
dig +short A proxy.example.com
dig +short AAAA proxy.example.com
```

Generate the client-facing secret on your own computer:

```bash
openssl rand -hex 16
```

That produces 32 lowercase hexadecimal characters. Keep the exact value: it is
entered in every client that uses this server and passed to the installer.

## 2. Prepare the real public site

The repository deliberately does not include a deployable public website. If many
operators installed the same starter, its body and assets would become an easy
active-probing signature. The recommended choice is a real loopback website or
stock static server configured with `public_upstream`. The in-process `public_dir`
mode remains available for simple sites.

The only required file is `index.html`. A several-page site will normally also
have `about.html`, `privacy.html`, `404.html`, `styles.css`, a favicon, and images.
Use exact links such as `/about.html`, or explicitly select `static_routes: "legacy"`
for extensionless aliases. Old configs retain those aliases for compatibility. See [`PUBLIC_SITE.md`](PUBLIC_SITE.md) for the complete
package contract and a prompt suitable for a site generator.

For a database-backed site, accounts, forms, server rendering, an existing CMS,
site APIs, SSE, or WebSockets, run any HTTP application on a numeric loopback
address such as `127.0.0.1:3000`. The relay can use it as `public_upstream` while
remaining the only public gateway. The application owns its framework, headers,
cookies, and persistence; unauthenticated requests on transport paths reach the
application unchanged apart from normal HTTP proxy semantics. This mode
is specified in [`PUBLIC_SITE.md`](PUBLIC_SITE.md).

## 3. Configure the hosting firewall

In the hosting provider's network rules or firewall, allow:

| Port | Source | Purpose |
|---:|---|---|
| TCP 22 | your administrator IP if possible | SSH |
| TCP 80 | anywhere | ACME validation and HTTPS redirect |
| TCP 443 | anywhere | website and WebView transport |

Do not allow TCP 2398, 8080, 8081, or 8888. The installer adds a local nftables rule
that drops external traffic to 2398 and 8888, but the provider firewall is the
second required boundary.

If the host itself runs UFW or another firewall, allow 80/443 there as well. Preserve
your working SSH rule before changing anything remotely:

```bash
sudo ufw status
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
```

## 4. Upload and install over SSH

From the parent directory on your computer, upload this working tree. `rsync` is
convenient even before the repository has a remote:

```bash
rsync -az --delete --exclude .git \
  tproxy-server/ YOUR_SSH_USER@YOUR_SERVER_PUBLIC_IP:/tmp/tproxy-server/
```

Then connect and run the installer, substituting the hostname and contact email:

```bash
ssh YOUR_SSH_USER@YOUR_SERVER_PUBLIC_IP
cd /tmp/tproxy-server
sudo ./deploy/install.sh \
  --hostname proxy.example.com \
  --email you@example.com \
  --site-dir ../my-site
```

For a local web application that is already running, use:

```bash
sudo ./deploy/install.sh \
  --hostname proxy.example.com \
  --email you@example.com \
  --site-upstream http://127.0.0.1:3000
```

The backend defaults to one official MTProxy worker and 4096 accepted client
connections per worker. On a measured multi-core deployment, the installer also
accepts `--mtproxy-workers N` and `--mtproxy-max-connections N`. Keep one worker for
the first deployment; raise it only while watching both MTProxy stats and relay CPU,
because Caddy and the Go relay need CPU on the same host.

The installer prompts without echo for the secret. Paste the exact value there.
This keeps it out of the shell history and process list.
For unattended provisioning, `--secret` is available, but it places the value in
the invoking process list and should be used only in automation where process
arguments are controlled.

The installer:

1. installs nftables and build prerequisites, a checksum-verified official Caddy
   binary with a dedicated systemd service, and a verified Go toolchain when the
   host does not already have Go 1.20 or newer;
2. verifies the archive for pinned MTProxy commit
   `f36d8af769ffaeac36978d38c2c0f6d1104c2137`, builds it unchanged as the
   unprivileged `mtproxy` user, and installs the result as root;
3. downloads the official MTProxy secret and routing configuration over HTTPS;
4. runs all Go tests and installs `/usr/local/bin/tproxy-server`;
5. installs the operator-supplied static site without overwriting an existing
   `/srv/tproxy-site`, or configures the private site application;
6. creates mode-restricted configuration and a systemd credential for the WEB
   secret;
7. installs the backend firewall, relay, MTProxy, refresh timer, and Caddy units; and
8. asks Caddy to obtain and renew the hostname's public certificate.

The script accepts lowercase hexadecimal secrets. The server configuration itself
also accepts canonical base64url secrets if you later manage profiles manually.

## 5. Verify the deployment

On the server:

```bash
systemctl --no-pager --full status \
  caddy tproxy-firewall mtproxy tproxy-server
curl --fail http://127.0.0.1:8081/healthz
curl --fail http://127.0.0.1:8081/readyz
curl --silent http://127.0.0.1:8081/metrics
ss -lntp
sudo nft list table inet tproxy_backend
```

Expected listeners are public Caddy on 80/443 and loopback relay listeners on
8080/8081. Official MTProxy listens on 2398 because its upstream command has no bind
address option; nftables must drop that port on every non-loopback interface.

From your own computer, verify the site and certificate:

```bash
curl --fail --show-error --location https://proxy.example.com/
curl --fail --show-error https://proxy.example.com/about
openssl s_client -connect proxy.example.com:443 \
  -servername proxy.example.com </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
```

Also confirm the local-only ports are unreachable externally. These commands should
time out or fail:

```bash
nc -vz -w 3 YOUR_SERVER_PUBLIC_IP 2398
nc -vz -w 3 YOUR_SERVER_PUBLIC_IP 8888
nc -vz -w 3 YOUR_SERVER_PUBLIC_IP 8080
nc -vz -w 3 YOUR_SERVER_PUBLIC_IP 8081
```

Check that missing, wrong, duplicated, or augmented bridge queries all display the
same normal home page:

```bash
curl --fail 'https://proxy.example.com/?bridge=wrong'
curl --fail 'https://proxy.example.com/?bridge=wrong&x=1'
```

Do not paste the real derived bridge URL into logs or test commands. A conforming
client derives it in memory.

## 6. Configure a Telegram client

A WEB-capable Telegram app accepts exactly two user-visible values:

```text
Hostname: proxy.example.com
Secret:   000102030405060708090a0b0c0d0e0f
```

The hostname field contains no `https://`, port, slash, query, or fragment. HTTPS
and port 443 are fixed by the WEB proxy type. Internationalized domains are stored
as lowercase ASCII IDNA A-labels. The secret is the same client-facing MTProxy
secret configured in the corresponding server profile.

A shareable WEB proxy link is:

```text
https://t.me/webproxy?server=proxy.example.com&secret=000102030405060708090a0b0c0d0e0f
```

Clients may also accept the equivalent `tg://webproxy` form. The public `t.me`
frontend does not yet register this route, so proof-of-concept testing may require
opening the link directly in the intended client.

Current client status:

- Telegram Desktop has a process-wide hidden native WebView carrier and an explicit
  system-browser fallback. Its full hosted test matrix is in
  `../tproxy/docs/web-proxy-test-plan.md`.
- The Android proof of concept uses a private, foreground-scoped Android System
  WebView. See [`ANDROID.md`](ANDROID.md) for its build and test instructions.
- The planned iOS proof of concept uses a process-wide `WKWebView` carrier. See
  [`IOS.md`](IOS.md) for its design and lifecycle plan.

These are client implementations of the same WEB proxy protocol, not separate
server modes.

### Repeating this on several hosting accounts

Give every server its own hostname, for example `north.example.com` and
`south.example.com`, and repeat the DNS, firewall, upload, and install steps on each
host. One relay process intentionally serves one public hostname. Independent
secrets make rotation and deployment management clearer; reusing a base secret is
technically possible because the bridge capability also includes the hostname, but
it couples all of those deployments to one credential.

## Manual integration on an existing Caddy server

Build and install the relay without running `deploy/install.sh`:

```bash
go test ./...
go build -trimpath -o tproxy-server ./cmd/tproxy-server
sudo install -m 0755 tproxy-server /usr/local/bin/tproxy-server
```

Install an operator-owned static site in `/srv/tproxy-site` or configure a local
web application as described in [`PUBLIC_SITE.md`](PUBLIC_SITE.md), then create
`/etc/tproxy-server/config.json` and a mode-`0400` profiles file from
`config.example.json` and `profiles.example.json`. When not using systemd
`LoadCredential`, point `profiles_file` directly at the mode-restricted file. Both
relay listeners, the public application, and every backend address must use
numeric loopback addresses. Provision the persistent signing key once (the
`tproxy` service user and `/etc/tproxy-server` must already exist):

```bash
sudo bash deploy/ensure-token-key.sh
```

Keep `/etc/tproxy-server/token.key` backed up, private, and unchanged across
restarts. `token_key_file` defaults to `token.key` next to the server config. A
missing or permissive key fails startup; no ephemeral key is silently generated.

Build the official backend with `deploy/install-mtproxy.sh`, install the supplied
systemd units, and let the relay serve the whole hostname:

```caddyfile
encode zstd gzip
header -Via
reverse_proxy 127.0.0.1:8080 {
  transport http {
    response_header_timeout 40s
  }
}
```

Do not route either static files or a site application around the relay on this
hostname. The relay must remain the one gateway so that encoding, response
headers, method handling, and reverse-proxy-hop timing do not expose a separate
transport surface. Set the server `timeouts` as in `deploy/Caddyfile` (`read_body`
well above `long_poll`).

The relay must receive the original `Host`. The supplied direct-to-origin Caddy
layout relies on Caddy's default sanitizing of forwarded client addresses; if you
change trusted-proxy handling, the relay must still receive exactly one IP address
in `X-Forwarded-For` for capacity accounting, and rejects a request that carries a
list or an unparsable value. Bootstrap tokens are not bound to that address because
a browser may use different VPN, carrier, or dual-stack egress connections to load
the bridge and create its session.
Do not apply your public site's
`X-Frame-Options`, COOP, COEP, or framing CSP to the proxied root response; the
bridge supplies a distinct CSP permitting only the numeric loopback parent.
Do not enable access logging of raw URIs, authorization headers, or bodies.

The generated bridge is self-contained and compatible with hardened native
WebViews: it requires only its nonce-bearing inline script, exact-origin HTTPS
Fetch and optionally same-origin WSS, timers, typed arrays, and the authenticated
app boundary. It does not use
cookies or browser storage, external resources, workers, frames, media, popups,
downloads, forms, device permissions, clipboard APIs, or cross-origin requests.
Carrier WebViews enforce response isolation, not a guarantee that the browser
engine emits no off-origin request: provider JavaScript must not be able to read
or use response data from anywhere except the exact proxy origin. Proxy bridges
must not attempt cross-origin requests; clients may block or cancel them, and
their network behavior is unspecified.
Keep the bridge response headers produced by the Go relay intact; `PROTOCOL.md`
lists the complete execution policy and explains which restrictions clients must
also enforce independently.

## Running alongside an existing website

The layout above gives the relay a hostname of its own and routes every path to
it. To put a relay on a domain that already serves a real site, or simply to move
the carrier off well-known root paths, configure a base path and route only that
prefix to the relay. [BASE_PATH.md](BASE_PATH.md) covers the two deployment modes,
the nginx and Caddy front-proxy configuration, slug generation, and rollout.

## Multiple secrets on one hostname

Add profiles to `/etc/tproxy-server/profiles.json`. Every profile has a unique name,
client secret, and numeric loopback backend:

```json
{
  "profiles": [
    {
      "name": "alpha",
      "secret": "0123456789abcdef0123456789abcdef",
      "backend": "127.0.0.1:2398",
      "carrier_mode": "https"
    },
    {
      "name": "beta",
      "secret": "fedcba9876543210fedcba9876543210",
      "backend": "127.0.0.1:2399",
      "carrier_mode": "https-lanes",
      "limits": {
        "max_sessions": 32,
        "max_streams": 512,
        "max_backend_dials_in_flight": 64,
        "new_sessions_per_minute": 120,
        "new_sessions_burst": 32,
        "new_streams_per_minute": 1200,
        "new_streams_burst": 128,
        "max_streams_per_session": 32,
        "max_pending_per_session": 8388608
      }
    }
  ]
}
```

`carrier_mode` is optional and defaults to `https` for compatibility:

| Mode | Carrier behavior | Primary tradeoff |
|---|---|---|
| `https` | one serialized POST plus one long poll | conservative baseline; one direction is capped near `carrier_batch_bytes / RTT` |
| `https-lanes` | independent POST sequence and long poll for every logical stream | mirrors Telegram TCP sessions and isolates latency; relies on HTTP/2 for many concurrent polls |
| `websocket` | one ordered WebSocket multiplexing all streams | removes HTTP stop-and-wait with one connection; all streams share its TCP congestion and failure domain |
| `websocket-lanes` | one ordered WebSocket for every logical stream | isolates browser and relay queues so bulk media does not block interactive streams; increases connection and handshake count |

The mode is selected through the secret/profile, so existing Desktop, Android, and
iOS proof-of-concept clients need no new setting or client-side transport code. Use
different secrets when exposing several modes on one hostname.

macOS WebKit can serialize WebSocket handshakes to the same host. With
`websocket-lanes`, a burst of main, media, and connection-test streams can therefore
take longer to open than the client's connection timeout. The bridge cancels a
pending handshake when the client closes that stream, but each remaining lane
still needs its own handshake. If connection establishment is slow or media
streams keep reconnecting, use `"carrier_mode": "websocket"` in the affected
profile in `/etc/tproxy-server/profiles.json`, then run
`sudo systemctl restart tproxy-server`. Clients reconnect using the new mode with
the same hostname and secret; the public website configuration needs no change.

Run one official MTProxy process and listener per profile when profiles need
separate quotas or routing. Extend `firewall.nft` to include every added backend
port. A single MTProxy may receive repeated `-S` arguments only when all profiles
intentionally share the same policy and routing scope.

### Capacity limits

The process-wide limits in `config.json` are the mandatory safety boundary. The
important admission controls are:

| Field | Default | Meaning |
|---|---:|---|
| `max_sessions_global` | 128 | live WebView carrier sessions |
| `max_streams_global` | 4096 | live backend TCP streams |
| `max_backend_dials_in_flight` | 256 | simultaneous backend connection attempts |
| `new_sessions_per_minute` / `new_sessions_burst` | 600 / 128 | process-wide session creation bucket |
| `new_streams_per_minute` / `new_streams_burst` | 6000 / 512 | process-wide stream creation bucket |
| `max_bootstraps_global` | 512 | live one-shot bridge bootstrap entries |
| `new_bootstraps_per_minute` / `new_bootstraps_burst` | 1200 / 256 | process-wide bootstrap creation bucket |
| `max_pending_global` / `max_pending_items_global` | 512 MiB / 262144 | process-wide buffered relay data and allocations |

`max_sessions_per_ip` and `max_bootstraps_per_ip` default to `0`, which disables
those optional hard limits. This is deliberate: many legitimate users may share a
carrier-grade NAT address. Set a positive value only when the deployment needs a
secondary source-address abuse boundary; it does not replace the global limits.

The relay refuses to start when the per-session control reserve, multiplied by
`max_sessions_global`, would leave `max_pending_global` or
`max_pending_items_global` with no room for data. `carrier_batch_bytes` may not
exceed 2 MiB, the desktop client's loopback-fallback message cap.

`timeouts.reconnect_grace` (default `2m`) is how long a session whose client has
stopped reaching the relay entirely stays resumable. An alive bridge refreshes the
session on every long poll, so it only matters after a total silence; the bridge's
own retry window fits under two minutes, and the desktop MTProto layer has already
reset its connections after ~30–45 s of silence, so a longer grace mostly pins
session slots for clients that will start a fresh session anyway. Raise it if
resume-over-slots is preferred (for example for mobile clients that background for
minutes). A failing bridge and a session created after the page closed both delete
their session promptly, so orphaned slots are bounded by this value.

Deployments that still serve Linux desktop proof-of-concept clients built before
the 2 MiB WebView message cap should set `limits.max_frame_payload` to `524288`
(512 KiB): that bounds both DATA coalescing and per-frame size so no native message
exceeds the older helper's 1 MiB cap; the throughput cost is negligible and the
default can be restored once those clients are updated.

Every `limits` value in a profile is optional. An omitted value inherits the
corresponding global value, so the default single profile adds no second quota.
Profile values may only lower the global ceiling. With several secrets, the global
buckets protect the whole process and each profile's session and stream buckets
prevent one secret from consuming more than its configured share. A stream rejected
by a capacity or creation-rate limit receives `CLOSE`; other streams and the parent
session remain active. Authenticated session creation overload, a temporarily full
uplink queue, and an uplink retry racing an in-flight parse all return HTTP 503 with
`Retry-After` so the bridge can retry safely; a downlink poll arriving while another
is parked simply supersedes it.

Official MTProxy has a separate process-level boundary. Its systemd service reads
`MTPROXY_WORKERS` and `MTPROXY_MAX_CONNECTIONS` from
`/etc/mtproxy/mtproxy.env`, defaulting to `1` and `4096`. The connection value is
passed to official MTProxy's per-worker `-C` limit. Keep the relay's
`max_streams_global` at or below the intended aggregate backend capacity and increase
workers only after one worker is CPU-bound; extra MTProxy workers do not accelerate
Caddy, the WebView bridge, or the Go relay.

Restarting `tproxy-server` invalidates active carrier sessions:

```bash
sudo systemctl restart tproxy-server
```

Existing TCP streams are intentionally not resumed across a relay restart. The
client recreates its WebView carrier and logical streams.

## Operations and updates

Useful diagnostics contain event classes and counts, not secrets or request URLs:

```bash
journalctl -u tproxy-server -u mtproxy -u caddy --since '30 minutes ago'
systemctl list-timers refresh-mtproxy-config.timer
curl --silent http://127.0.0.1:8888/stats
```

Go profiling routes are disabled by default. Set `"enable_pprof": true` only for a
bounded diagnostic window on a host where the loopback admin listener is trusted,
then disable it and restart the relay.

The supplied service units use `ProtectProc=invisible` and `ProcSubset=pid`, so the
public Caddy and relay users cannot inspect other services' process metadata. Stock
MTProxy accepts its secret through `-S`, which leaves the value in that process's
argument memory. Root and unrestricted host administrators can still inspect it;
do not give untrusted users login or unconfined service access to this host.

The refresh timer fetches `proxy-multi.conf` daily and restarts MTProxy only when
the routing data actually changed. Existing backend streams reconnect through the
still-live relay session. The public website remains available when either
backend process is down; `/readyz` reports the backend outage.

`tproxy-firewall.service` is ordered after and bound to `nftables.service`, so a
distribution `nftables` start/reload (whose default `/etc/nftables.conf` begins
with `flush ruleset`) re-applies the `inet tproxy_backend` table instead of
silently leaving MTProxy's `0.0.0.0` listener exposed. The hosting provider's
firewall from step 3 remains the first boundary; this unit is the second.

Never enable access logging of raw URIs, request headers, or bodies on Caddy or
the relay: the bridge URL carries the derived capability, and the WebSocket
carrier's session bearer travels in `Sec-WebSocket-Protocol`.

For a relay-code update, upload the new repository and run from its root:

```bash
sudo ./deploy/update-relay.sh
```

The updater finds the installed Go toolchain, runs all Go tests, builds and validates
a candidate against the installed configuration, keeps the previous binary, installs
the candidate atomically, and restarts only `tproxy-server`. It waits for health and,
when the old deployment was ready, backend readiness; a failure automatically rolls
back to the previous binary. Existing carrier sessions are invalidated; clients
must obtain a fresh bridge page and relay session automatically.

The updater provisions a missing default `token.key` without changing existing
config JSON, so an older binary can still be restored. On that first migration it
also installs a dedicated systemd environment drop-in to drain legacy tokens
safely. Remove the drop-in once existing clients have reloaded, as described in
[HARDENING.md](HARDENING.md), to enable complete public pass-through. Existing key
bytes are preserved. For a custom `token_key_file`, provision that path and arrange
legacy draining separately.

Apart from the first-migration drop-in, this script does not replace configuration,
systemd units, Caddy, MTProxy, firewall rules, or public-site files. Running the complete automated
installer again preserves an existing site directory but replaces the single-profile
config and active Caddyfile, so use it deliberately.

The probing-hardening change also has a separate Caddy migration; see
[HARDENING.md](HARDENING.md) for the reviewed changes, token rollout limitations,
and verification commands. Do not rerun the full installer just to update Caddy.

When an update includes reviewed changes to the supplied relay or MTProxy unit,
install those units separately and then restart the affected services:

```bash
sudo install -m 0644 deploy/tproxy-server.service \
  /etc/systemd/system/tproxy-server.service
sudo install -m 0644 deploy/mtproxy.service \
  /etc/systemd/system/mtproxy.service
sudo systemctl daemon-reload
sudo systemctl restart mtproxy tproxy-server
```

The new unit defaults remain compatible with an existing `mtproxy.env` containing
only `MTPROXY_SECRET`; add `MTPROXY_WORKERS` and `MTPROXY_MAX_CONNECTIONS` there only
when overriding the defaults.

## Troubleshooting

- **Caddy cannot obtain a certificate:** confirm the `A`/`AAAA` records point
  directly to this host and that both 80 and 443 reach Caddy. Remove a broken AAAA
  record rather than leaving IPv6 half-configured.
- **`/readyz` returns 503:** inspect `systemctl status mtproxy`, then confirm a local
  TCP connection to `127.0.0.1:2398` and the downloaded files under `/etc/mtproxy`.
- **The WebView shows the public site instead of connecting:** hostname and secret
  must match the server profile exactly; the client derives a different capability
  for every hostname/secret pair.
- **The client remains in its connecting state:** confirm the WebView can load the
  exact HTTPS hostname, then use the platform-specific client document for native
  bridge, lifecycle, and fallback diagnostics.
- **The public site works but the bridge fails:** inspect only sanitized service
  status and metrics. Never log bridge URLs or authorization headers.
- **Configuration check fails on permissions:** the profiles file must have no group
  or other permission bits. Use `chmod 0400` and ensure the service receives it via
  `LoadCredential`.

The complete architecture and implementation milestones remain in `PLAN.md`; the
normative wire format is in `PROTOCOL.md`.

## Docker Compose with Cloudflare Tunnel

The Compose stack publishes no host ports. The relay listens on port 8080 only
inside the private `tunnel` network, and `cloudflared` is the only HTTP ingress.
Cloudflared also joins a separate outbound network so it can reach Cloudflare;
the relay itself has no direct Internet egress.
The admin listener remains on loopback inside the relay container and is not
reachable from the tunnel container.

Before the first start, copy `.env.example` to `.env`, set the token issued on the
Cloudflare Zero Trust tunnel page, and set the same public hostname in
`TPROXY_HOSTNAME` and `public_hostname` inside `docker/config.json`. Replace the
example secret in `docker/profiles.json` with the same 16-byte hexadecimal
MTProxy secret used by the client:

```bash
cp .env.example .env
docker compose config
docker compose build
docker compose run --rm tproxy-server -config /etc/tproxy-server/config.json -check
docker compose up -d
docker compose ps
docker compose logs --tail=100 tproxy-server
docker compose logs --tail=100 cloudflared
```

In the Cloudflare tunnel configuration, create a Public Hostname whose service is
`http://tproxy-server:8080`. Cloudflare terminates public HTTPS; Caddy is not part
of this stack. Do not add a Compose `ports` mapping for the relay.

### Setup

Create `.env`:

```bash
cp .env.example .env
```

Configure it:

```dotenv
TPROXY_HOSTNAME=proxy.example.com
CLOUDFLARE_TUNNEL_TOKEN=token-issued-by-cloudflare
```

Set the same hostname in [`docker/config.json`](docker/config.json):

```json
"public_hostname": "proxy.example.com"
```

Configure this service URL for the Public Hostname in Cloudflare:

```text
http://tproxy-server:8080
```

Start the stack:

```bash
docker compose down --remove-orphans
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 cloudflared
docker compose logs --tail=100 tproxy-server
```

Ports 80, 443, and 8080 do not need to be opened or published on the target host.
