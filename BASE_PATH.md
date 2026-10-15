# Base path deployments

A relay may serve its whole surface under a path prefix instead of the host root,
so one hostname can run an ordinary website *and* a relay without the relay
becoming the gateway for the entire site.

**Status.** Implemented, in this server and in Telegram Desktop. §7 records what
the implementation covers and what a rollout should check.

## 1. Wire layout

Let `H` be the hostname and `P` the base path (empty at the root):

```text
base = P is empty ? "/" : "/" + P + "/"
```

```text
P empty (today, unchanged):     P = t7k2xw:
  GET  /?bridge=…                 GET  /t7k2xw/?bridge=…
  POST /api/v1/session            POST /t7k2xw/api/v1/session
  POST /api/v1/up                 POST /t7k2xw/api/v1/up
  POST /api/v1/down               POST /t7k2xw/api/v1/down
  POST /api/v1/stream             POST /t7k2xw/api/v1/stream
  GET  /api/v1/ws                 GET  /t7k2xw/api/v1/ws
```

Only the trailing-slash form is served. `/<P>` without the slash is not
special-cased and gets no redirect — a redirect would be one more branch an
observer can distinguish.

The capability binds to the path, so one minted for a prefix authenticates nothing
at another prefix or at the root:

```text
context = P is empty
            ? UTF-8("tdesktop-web-proxy-bridge-v1\n" + H)
            : UTF-8("tdesktop-web-proxy-bridge-v2\n" + H + "\n" + P)
bridge  = base64url-no-padding(HMAC-SHA256(key = secret, message = context))
```

The root context is the frozen v1 one, byte for byte, so existing deployments and
existing clients are unaffected.

| Host | Base path | Secret (hex) | `bridge` |
|---|---|---|---|
| `proxy.example.com` | — | `000102030405060708090a0b0c0d0e0f` | `MHLEY5PmW1GWqJkSrlmJpvJUiLhBH_QKy6yKg8a0JPk` |
| `proxy.example.com` | — | `dd000102030405060708090a0b0c0d0e0f` | `IpJrt3e7sKtzPyoXy6w-Zj6GGEvsvclN66JzQEfPYLA` |
| `proxy.example.com` | `dobry-cola-super-app` | `000102030405060708090a0b0c0d0e0f` | `hHz99Xs93EN1j91G9gpNepXwGNNt5YdAFkEVk_LlqdQ` |
| `proxy.example.com` | `dobry-cola-super-app` | `dd000102030405060708090a0b0c0d0e0f` | `TGUkZaevsavLbHvlNWipnRoYxgzZ51ioWvbxgGT3wHo` |

### Path syntax

One or more `/`-separated segments, each `[A-Za-z0-9][A-Za-z0-9_-]*`, at most 128
characters in total, stored with no leading or trailing slash. `.` is not in the
alphabet, so `.` and `..` segments cannot occur and no dot-segment resolution is
ever needed. Empty segments (`a//b`), `%xx` escapes and non-ASCII are rejected
rather than repaired, so the configured value and the wire path are the same
string. **The path is case-sensitive** and is never folded — unlike the hostname,
which is case-insensitive and stored lowercased.

## 2. Two deployment modes

The prefix is orthogonal to who owns the hostname. Both modes are supported and
they answer different questions.

| | **A — relay owns the hostname** | **B — production co-hosting** |
|---|---|---|
| Front proxy | everything to the relay | only `/<P>/` to the relay |
| Site | relay's `public_dir` / `public_upstream` | served directly, untouched |
| Stack uniformity | one gateway, one response stack | two stacks on one hostname |
| Site keeps its own WebSockets, streaming, cache, pooling | no | yes |
| `/api/v1/*` may collide with real site routes | no (with a prefix) | no |
| Recommended for | a hostname dedicated to the proxy | a real production domain |

**Mode A is the recommended default, prefix included.** The relay still receives
every request, so nothing about the public surface changes — but the carrier is no
longer at well-known root locations, an untargeted scanner never touches it, and a
site route named `/api/v1/…` can no longer collide with the transport. It costs
nothing but a longer bridge URL.

**Mode B** is what makes a real production domain usable at all: without it, the
relay must reverse-proxy the whole site, which means a new failure domain, no
upstream connection pooling, stripped `Upgrade` headers, and reserved root paths.

Honest limits of Mode B: two response stacks answer on one hostname, so once the
prefix is known an observer can compare a normal site response against a
relay-forwarded one. That condition is not met while the link is distributed
privately (§3), which is what makes Mode B strong for a small trusted group and
weaker for a publicly posted link. Keep the two branches as similar as you can
(§4), and use Mode A when uniformity matters more than co-hosting.

## 3. Configuration

```json
{
  "public_hostname": "example.com",
  "base_path": "phcf2vfe7zgbrslg",
  "listen": "127.0.0.1:8080",
  "admin_listen": "127.0.0.1:8081",
  "public_upstream": "http://127.0.0.1:3000",
  "token_key_file": "/etc/tproxy-server/token.key",
  "profiles_file": "/etc/tproxy-server/profiles.json"
}
```

One `base_path` per vhost, not per profile and not a list of aliases: the runtime
identifies a vhost by hostname, and several prefixes on one host would complicate
session ownership and routing for nothing. Omitted or empty preserves today's root
behavior exactly. Changing it changes every capability on that host, so existing
client links stop working and live sessions drop — treat it as a re-issue.

Any path valid under §1 is accepted, and clients enforce nothing beyond that
rule: a human-readable prefix, mixed case and several segments are all equally
correct. What follows is only what this server generates when the operator does
not pick one.

The default is lowercase RFC 4648 base32 of 10 random bytes: 16 characters, 80
bits, every character inside the allowed alphabet and a valid first character by
construction. Ten bytes is a multiple of five, so the encoding is exact — no
padding to strip and no partial final character.

```bash
head -c 10 /dev/urandom | base32 | tr 'A-Z' 'a-z'
# phcf2vfe7zgbrslg
```

Eighty bits is far more than the job needs, because guessing the prefix has no
oracle: without a capability, a correct guess returns the site's own 404 for that
path, byte for byte identical to any wrong guess, so an attacker cannot even tell
a hit from a miss. The prefix earns its length by not colliding with the site's
real routes, not by resisting brute force.

Unpadded base64url is also in-alphabet and shorter, but about 3%
of its outputs begin with `-` or `_`, which the first-character rule rejects and a
generator would have to retry; base32 has no such case, so this generator uses
it. Standard base64 is not usable at all — `+`, `/` and `=` are outside the
alphabet — and base64 must never be lowercased.

Length is not the interesting variable: guessing the prefix has no oracle at all,
and no scanner fuzzes 80 bits blind. How the link is distributed is what decides
the prefix's value.

Shared privately — a family, a handful of friends — the prefix is a real second
unknown. Someone probing the hostname finds the website and nothing else: no
proxy-shaped host, no `/?bridge=`, no `/api/v1/*` to fingerprint. Combined with
Mode B this is the strongest layout available, because the hostname is a genuine
production site and blocking it costs the blocker that site.

Posted publicly, the prefix stops being protection — but so does the hostname,
which already tells an observer that this host runs a relay. Losing the prefix is
not the interesting loss at that point.

Either way the prefix is never authorization. The relay must keep treating the
bootstrap capability, session bearer and MTProxy secret as the only credentials,
and a request that knows the prefix and nothing else gets the public site.

A plausible prefix such as `dobry-cola-super-app` blends into a real site's own
access logs better than a long random string, which matters when a third party
reads them — a hosting control panel, an analytics vendor, a CDN. It is guessable
by a human who thinks about it, so it trades scanner resistance for that.

The client link carries the whole address in one percent-encoded `server`
parameter, and encodes its secret as unpadded base64url of the byte `0x70`
followed by the real secret:

```text
tg://webproxy?server=example.com%2Fphcf2vfe7zgbrslg&secret=<marked>
https://t.me/webproxy?server=example.com%2Fphcf2vfe7zgbrslg&secret=<marked>
```

```bash
# <marked> from the secret in profiles.json, as deploy/install.sh derives it.
# xxd is deliberately not used: it ships with vim-common, which a clean server
# need not have. The raw secret is piped, never captured, because command
# substitution drops NUL bytes.
{ printf '\x70'; printf "$(printf %s "$secret" | sed 's/../\\x&/g')"; } \
  | base64 | tr '+/' '-_' | tr -d '=\n'
# 8561944064fc730cbfa4473562d8ec59 -> cIVhlEBk_HMMv6RHNWLY7Fk
```

A client decodes by the inverse rule: base64url-decode, and if the result is at
least 17 bytes and starts with `0x70`, strip that byte and use the rest;
otherwise use the value as it stands. That is unambiguous because a canonical
secret is 16 bytes, 17 starting with `0xDD`, or 21+ starting with `0xEE`. A link
carrying a base path must use the marked form — an unmarked secret there is
rejected, so no link exists that an older client would take for a pathless proxy
on an empty host.

The marker exists because a client without base path support normalizes
`host/path` to an empty host, finds nothing else wrong, and offers to connect to
it. With the marked secret such a client instead decodes 17 bytes not starting
with `0xDD`, reports an unsupported proxy type and asks the user to update. Never
use `0xDD` as the marker: it is read as an ordinary padded secret and accepted. A
root link keeps the plain secret, so older clients keep working with it.

Installer surface: `--base-path <slug>` to pin one, `--base-path none` for the
root, and a freshly generated random slug when the flag is absent on a new
install. An existing config without `base_path` keeps serving the root.

## 4. Mode B: nginx in front of an existing website

The website keeps its own server block; one prefix location goes to the relay.

```nginx
# Enables upstream keepalive for ordinary requests while still forwarding the
# WebSocket upgrade for /api/v1/ws.
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      "";
}

upstream tproxy_relay {
    server 127.0.0.1:8080;
    keepalive 32;
}

server {
    listen 443 ssl;
    http2 on;                   # nginx < 1.25.1: `listen 443 ssl http2;`
    server_name example.com;

    # TLS, HSTS, compression and security headers stay at the server level so
    # both branches answer with the same envelope.
    gzip on;

    # Carrier uplink batches reach 2 MiB; nginx defaults to 1 MiB and would
    # break the transport with 413.
    client_max_body_size 4m;

    location ^~ /phcf2vfe7zgbrslg/ {
        # No URI part after the upstream name: the original path, including the
        # prefix, is forwarded unchanged. Do NOT write `.../;` here.
        proxy_pass http://tproxy_relay;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        # Exactly one address. $proxy_add_x_forwarded_for appends and can produce
        # a list, which the relay rejects.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # Parked long polls must not be cut or held back.
        proxy_read_timeout 90s;     # relay long_poll is 25s
        proxy_send_timeout 90s;
        proxy_buffering off;
        proxy_request_buffering off;

        access_log off;             # never log carrier URIs or Authorization
    }

    location / {
        proxy_pass http://127.0.0.1:3000;   # the real website
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

`^~` stops regex locations from stealing the prefix. A request to
`/phcf2vfe7zgbrslg` without the trailing slash does not match and is
answered by the website — which is exactly the intended behavior.

### The decoy inside the prefix

Requests under the prefix that carry no authentic secret still reach the relay,
and it answers them from `public_dir` / `public_upstream`. Point
`public_upstream` at the **same backend nginx serves at `/`**, so a probe of
`/<P>/whatever` gets the website's own 404 for that path — identical to what any
other unknown path returns.

The relay forwards the original path, prefix included, and never strips it. A
stripped prefix would make the whole site reachable a second time under `/<P>/…`,
which is a far louder signature than the 404 it replaces.

If the site is static files served by nginx itself, give the relay a loopback
mirror of the same root:

```nginx
server {
    listen 127.0.0.1:3000;
    server_name _;
    root /srv/www/example.com;
    index index.html;
    error_page 404 /404.html;
}
```

Without such a mirror the in-prefix 404s come from the relay instead of the site
and will not match it — a distinguisher, and the main reason to prefer Mode A when
the site cannot be reached over loopback.

### Caddy equivalent

```caddyfile
example.com {
	encode zstd gzip
	header -Via

	handle /phcf2vfe7zgbrslg/* {
		reverse_proxy 127.0.0.1:8080 {
			flush_interval -1
			transport http {
				response_header_timeout 40s
			}
		}
	}

	handle {
		reverse_proxy 127.0.0.1:3000
	}
}
```

Keep the global `servers.timeouts.read_body` well above the relay's `long_poll`,
as in `deploy/Caddyfile`.

## 5. Rolling out on a domain you already own

1. Keep the website exactly where it is, reachable on loopback (`127.0.0.1:3000`
   directly, or the mirror block above for static files).
2. Generate the slug (§3) and the client secret (`openssl rand -hex 16`).
3. Install the relay and the stock MTProxy backend as in `README.md`, with
   `base_path` set and `public_upstream` pointing at that loopback website.
4. Add the prefix location to the existing nginx/Caddy site (§4). No DNS, TLS or
   certificate change: it is the same hostname.
5. Verify, then hand out
   `tg://webproxy?server=example.com%2F<slug>&secret=<secret>`.

Verification, all against the live hostname:

```bash
curl -sI https://example.com/                     # the website, unchanged
curl -sI https://example.com/<slug>/              # website 404, no relay tell
curl -sI https://example.com/<slug>/api/v1/ws     # website 404
curl -sI https://example.com/api/v1/session       # website response, not the relay
```

Then load the real bridge URL with a valid capability and confirm a 200 HTML
response, and connect a client end to end.

## 6. What does not change

Frames, sessions, tokens, carrier modes, limits, timeouts, decoy policy,
capability scanning of request metadata, the loopback-only listeners, the
`X-Forwarded-For` single-address rule, and every response header of the bridge.
A root deployment is byte-identical to today.

## 7. What the implementation covers

Code:

- `internal/config`: `base_path` field, canonical validation, and `DeriveCapability`
  taking the path and selecting the v1 or v2 context.
- `internal/server/server.go`: `isTransportPath` becomes base-relative;
  `bridgeProfile` requires `EscapedPath() == base`; the credential gate in
  `hasInternalSecret` stays first and unchanged, so an authentic secret is still
  never delegated to the public application, wherever in the request it appears;
  `servePublic` keeps receiving the original, unstripped path.
- `internal/bridge/page.go`: build carrier URLs from `relayOrigin + base`, not
  `relayOrigin + "/"`. Without this a prefixed deployment bootstraps correctly and
  then requests the carrier at the root.
- `deploy/install.sh`: `--base-path <slug|none>` accepting any §1-valid path, and
  defaulting on a new install to a generated 16-character base32 slug (§3),
  written into `config.json` and echoed in the client address as `host/slug`.

Tests: prefix routing for bridge and all four carrier paths; root regression;
cross-prefix and root-vs-prefix capability rejection; a prefix request with no
secret reaching the public handler with its path intact; `/<P>` without the
trailing slash not being special-cased; `a//b` and `%2F` forms rejected.

Docs: `PROTOCOL.md` §"Bridge URL" (base, v2 context, the four vectors),
`README.md` (a co-hosting section pointing here), `PUBLIC_SITE.md` (its
"send all paths through the relay" rule is Mode A; Mode B is the exception),
`config.example.json`.
