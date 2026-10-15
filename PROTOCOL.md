# WEB proxy protocol v1

This is the client-independent wire contract shared by a WEB-capable Telegram app,
the HTTPS bridge page, and `tproxy-server`. All binary integers are unsigned and
big-endian.

A client applies Telegram's normal MTProxy transform first. Its local WEB adapter
maps every resulting TCP connection to a logical stream and multiplexes those
streams through one WebView carrier session. The bridge page converts complete
shared-frame batches into the profile-selected carrier; the relay converts each
logical stream back into one TCP connection to its configured stock MTProxy. DATA
payloads are opaque at every layer after the app's MTProxy transform.

## Bridge URL

The user configures a canonical lowercase ASCII/IDNA hostname `H` and an MTProxy
secret. Decode the secret to bytes `S`, retaining the leading `dd` byte when random
padding mode is selected, then derive:

```text
context = UTF-8("tdesktop-web-proxy-bridge-v1\n" + H)
bridge = base64url-no-padding(HMAC-SHA256(key=S, message=context))
URL = https://H/?bridge=bridge
```

Vectors:

| Host | Secret bytes in hex | Bridge capability |
|---|---|---|
| `proxy.example.com` | `000102030405060708090a0b0c0d0e0f` | `MHLEY5PmW1GWqJkSrlmJpvJUiLhBH_QKy6yKg8a0JPk` |
| `proxy.example.com` | `dd000102030405060708090a0b0c0d0e0f` | `IpJrt3e7sKtzPyoXy6w-Zj6GGEvsvclN66JzQEfPYLA` |

Only an exact `GET /` with one canonical 43-character `bridge` parameter selects
the bridge. Requests with no authentic relay secret follow the public website.
An authentic capability in a noncanonical query or on the wrong path fails
locally with an uncacheable 404, without exposing it to the public application.

`tdesktop-web-proxy-bridge-v1` is a frozen v1 domain-separation label. Its name is
retained for compatibility and does not restrict the protocol to Telegram Desktop.

A relay may also serve its whole surface under a base path, so that `GET /` above
becomes `GET /<base-path>/` and every `/api/v1/…` endpoint moves under the same
prefix. That variant derives its capability from a `tdesktop-web-proxy-bridge-v2`
context binding both host and path, leaving the root derivation byte-identical.
See [BASE_PATH.md](BASE_PATH.md).

## Client-to-bridge boundary

The bridge supports two ways to connect a Telegram app to the same carrier logic.
A normal native implementation uses the injected WebView boundary. The loopback
parent boundary is retained for clients that deliberately use a system browser as
a carrier or fallback. Neither boundary changes the HTTP or shared-frame protocol.

### Injected WebView boundary

The app loads the bridge document as the WebView main frame and appends a
client-only fragment:

```text
https://H/?bridge=bridge#android=webview-nonce
```

`webview-nonce` is 32 random bytes in canonical unpadded base64url form. The
`android` fragment key is a frozen v1 compatibility name used by all current
reference clients; it does not identify the client platform. URL fragments are not
sent in the HTTPS request.

Before navigation, the app exposes a page object named `TelegramWebProxy` only to
the exact `https://H` main frame. The object has a `postMessage(value)` method used
by the page and an `onmessage` callback used by the app. The platform binding must
authenticate the active WebView, main frame, exact origin, current navigation, and
nonce. Wildcard origins and unrestricted JavaScript interfaces are not conforming.

The bridge removes the query and fragment with `history.replaceState`, adapts the
object to its internal port contract, and sends this JSON control value:

```json
{"t":"tproxy-android-init","v":1,"nonce":"webview-nonce"}
```

`tproxy-android-init` is also a frozen v1 compatibility name. The app accepts it
only when the nonce and authenticated WebView context match. Messages from the
bridge to the app are either JSON control values or one complete shared frame.
Messages from the app to the bridge use the same representations. A platform may
use ArrayBuffer, a shared native buffer, or a private base64 envelope across its
WebView IPC boundary; that private encoding is removed before the value reaches the
bridge and is not part of the selected carrier or shared-frame format.

The bridge splits an aggregated HTTP downlink body at validated frame boundaries
before sending frames through this direct WebView boundary. Clients should keep
DATA frames at or below the relay's 64 KiB chunk size.

Each platform binds this object through its origin-scoped native WebView API. Some
bindings carry ArrayBuffers directly; others use a private string/base64 or shared
buffer shim. The platform documents linked below describe those implementation
choices without changing this boundary.

### Loopback parent boundary

A client-controlled loopback page may embed the HTTPS bridge page and transfer one
`MessagePort`:

```javascript
iframe.contentWindow.postMessage(
  {t: 'tproxy-init', v: 1},
  'https://proxy.example.com',
  [channel.port2]
);
```

The bridge accepts this once, only from its parent, only with the exact object and
one port, and only when `event.origin` is an explicit
`http://127.0.0.1:<port>` origin. Binary MessagePort messages are complete carrier
batches. Control objects are `{t:'status',state}`, diagnostic
`{t:'traffic',up,down}` byte counts, and `{t:'close'}`. Traffic counts are
nonnegative numbers describing the completed carrier operation; clients may
discard them after validation.

### Hardened WebView execution profile

The reference bridge is deliberately compatible with a private WebView whose
network security contract lets the remote document read or use response data only
from the exact proxy origin. This is not a guarantee that the browser engine emits
no off-origin request. Proxy documents must not attempt cross-origin requests;
clients may block or cancel them, and their network behavior is unspecified. The
bridge uses Fetch, `AbortController`, timers, same-document history replacement,
typed arrays, and one authenticated native or `MessagePort` boundary. Its HTTPS
requests use `mode: 'same-origin'`, `credentials: 'omit'`, `cache: 'no-store'`,
`redirect: 'error'`, and `referrerPolicy: 'no-referrer'`.

The bridge has no external scripts, styles, fonts, images, frames, objects, media,
or manifests. It does not use cookies, DOM storage, IndexedDB, Cache Storage,
service workers, dedicated/shared workers, popups, downloads, forms, WebRTC, device
permissions, clipboard access, or cross-origin requests. A client may therefore
disable those facilities without changing the carrier protocol. It must still
allow the nonce-bearing inline script, exact-origin HTTPS requests, same-origin WSS
when the profile selects `websocket` or `websocket-lanes`, ordinary timers, and
the selected authenticated client boundary.

The reference response enforces the same profile with this CSP:

```text
default-src 'none';
base-uri 'none';
child-src 'none';
connect-src 'self' wss://H;
font-src 'none';
form-action 'none';
frame-ancestors http://127.0.0.1:*;
frame-src 'none';
img-src 'none';
manifest-src 'none';
media-src 'none';
object-src 'none';
script-src 'nonce-<per-response nonce>';
style-src 'none';
worker-src 'none';
sandbox allow-same-origin allow-scripts
```

`allow-scripts` is required because the carrier is implemented by the page script.
`allow-same-origin` is required so Fetch sends the exact `https://H` origin instead
of an opaque `null` origin. The numeric-loopback `frame-ancestors` source preserves
the optional browser carrier without allowing arbitrary sites to frame the bridge.

The response also sends `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
`X-Content-Type-Options: nosniff`, `X-DNS-Prefetch-Control: off`, and a
`Permissions-Policy` that denies autoplay, capture devices, sensors, clipboard,
display capture, payment, wake lock, USB/HID/serial, and the other browser features
unused by the bridge. It deliberately does not send `X-Frame-Options`, COOP, or
COEP because those can prevent the loopback parent boundary from working.

These response headers describe and protect the reference implementation. A
hardened Telegram carrier must independently impose its own navigation, response
isolation, best-effort request blocking, storage, media, and permission
restrictions because a different proxy operator controls its own document and
response headers.

## Carrier modes

Each server profile selects one `carrier_mode`: `https`, `https-lanes`,
`websocket`, or `websocket-lanes`. Omitting it preserves the original `https`
mode. The choice is embedded in the generated bridge page and fixed for the
resulting relay session; clients do not need a new setting or protocol
implementation.

On the public TLS listener, `https` and `https-lanes` use ordinary HTTP request
and response semantics (typically multiplexed over ALPN `h2` by Caddy) with no
HTTP/1.1 `Upgrade: websocket` / `101 Switching Protocols` transition. By
contrast, `websocket` and `websocket-lanes` perform an RFC 6455 upgrade and
maintain long-lived bidirectional WebSocket connections, which stateful DPI
middleboxes (including TSPU) can classify and throttle after roughly 2 MiB of
transfer. Deployments serving clients on such paths should select `https-lanes`
or `https`. Because middleboxes treat `HTTP/1.1 101` differently from standard
`POST`/`GET` requests, `carrier_mode` is strictly a server-profile property and
clients must never self-negotiate or override the profile's configured mode.

Carrier requests normally have `Origin: https://H`, but the reference relay does
not authenticate that header: native WebViews may omit it, while non-browser
clients can spoof it. Authentication comes from the unguessable bootstrap or
session bearer. HTTP carrier requests have no cookies, and the relay rejects a
cookie-bearing HTTP API request; the WebSocket upgrade is exempt because a
browser's `WebSocket` constructor cannot omit cookies the site may have set.
Binary HTTP bodies use exactly `Content-Type: application/octet-stream`.

Requests with no authentic secret follow the ordinary public handler, including
the original request body and random bearer values. Authentic tokens used with a
wrong method, malformed headers, or missing session state fail locally with an
uncacheable 404. Their bodies remain under the carrier read deadline. A create
body is a single `HELLO` frame, so the relay caps it at 64 bytes.

Session creation exchanges a two-minute bootstrap token atomically and
idempotently:

```text
POST /api/v1/session
Authorization: Bearer bootstrap-token
Body: one HELLO frame

200 OK
X-Session-Token: session-token
X-Down-Cursor: 0
X-Carrier-Mode: https | https-lanes | websocket | websocket-lanes
Body: one WELCOME frame
```

The bootstrap is a bearer capability rather than a source-address-bound token.
Browser requests may switch VPN, carrier, load-balancer, or dual-stack egress
between loading the bridge, creating the session, and retrying that creation.
The relay still accounts the bootstrap against its issuing address and the
created session against the address that submits the first valid creation request.

After a valid bootstrap is authenticated, temporary session-capacity or
creation-rate exhaustion returns `503 Service Unavailable` with `Retry-After: 1`.
The bootstrap remains unconsumed so the byte-identical creation request can retry.

### Token representation and restart behavior

Bootstrap and session tokens remain opaque 32-byte values encoded as 43 canonical
unpadded base64url characters. Their server-side layout is a 16-byte nonce
followed by a 16-byte truncated HMAC-SHA256. The MAC input is
`"tproxy-server-token-v1\x00" || kind || nonce`, where kind is byte 1 for bootstrap
and byte 2 for session. The nonce is 12 random bytes and a 4-byte filter tag: the
first 4 bytes of AES-128, keyed with the first 16 bytes of
HMAC-SHA256(signing key, `"tproxy-server-token-filter-v1\x00"`), over the random
bytes padded with zeros. The relay checks every base64 offset of unauthenticated
request metadata for tokens; the keyed tag lets it skip the HMACs for all but
2^-32 of those candidates. The independent 32-byte signing key persists across
relay restarts.

Earlier releases drew all 16 nonce bytes at random and check only the MAC, so
they recognize tagged tokens after a rollback. In the other direction, the relay
verifies the MAC without the tag in `Authorization` bearers and carrier WebSocket
subprotocols, where the bridge page sends tokens, so untagged tokens from before
the upgrade fail locally and the page reloads instead of reaching the website.
Elsewhere in request metadata only tagged tokens are recognized. Clients must not interpret the layout; all existing endpoint names,
headers, capability derivation, and four carrier modes are unchanged.

A valid MAC establishes provenance only. Session state, bootstrap expiry, replay,
method, body, and capacity checks still authorize each operation. A restart
invalidates live state as before; clients reload the bridge and recreate streams.
Signed expired or wrong-kind tokens are rejected locally, never delegated to the
website. Pre-migration random tokens have no verifiable provenance after restart;
see [HARDENING.md](HARDENING.md) before the first rollout.

### Serialized HTTPS

In `https` mode, uplink requests are serialized. `X-Up-Seq` begins at `1`. The relay accepts the
next sequence or a byte-identical retry of the last committed sequence:

```text
POST /api/v1/up
Authorization: Bearer session-token
X-Up-Seq: 1
Body: one or more complete frames

204 No Content
X-Up-Ack: 1
```

If the next valid batch cannot yet fit the relay's DATA queue budget, or a retry
of the next sequence arrives while the relay is still parsing the previous request
for it, the relay returns `503 Service Unavailable` with `Retry-After: 1`. The
sequence remains uncommitted and no frame from the batch is applied. The bridge
retries the same sequence with the byte-identical body after honouring
`Retry-After` (a fixed retry count never applies to 503; a 90-second budget does).

One downlink poll is active at a time and the newest poll wins: when a poll arrives
while another one is parked (typically because the older connection died silently),
the newer poll takes over and the older one completes as `204 No Content` with its
own cursor — harmless if that connection is still alive, unobserved if it is dead.
A poll is never refused for being concurrent. The cursor acknowledges a previously
delivered batch. Repeating the old cursor replays the unacknowledged batch
byte-for-byte:

```text
POST /api/v1/down
Authorization: Bearer session-token
X-Down-Cursor: 0
Empty body

200 OK                       204 No Content
X-Down-Cursor: 1             X-Down-Cursor: 0
Body: complete frame batch   Empty body
```

The uplink POST and downlink poll run concurrently. With a 2 MiB carrier batch, a
continuously busy direction has an application-level ceiling of approximately
`2 MiB / carrier RTT`.

### Stream-aware HTTPS lanes

`https-lanes` keeps the same endpoints and retry rules but assigns one independent
lane to each shared stream. Every `/up` and `/down` request adds:

```text
X-Lane-ID: <decimal shared stream id>
```

Lane zero is reserved for session-level PONG traffic. For a nonzero lane, every
frame in an uplink body and every frame returned by its downlink poll must have a
`stream_id` equal to `X-Lane-ID`. A new lane must begin with `OPEN`. Each lane has
its own `X-Up-Seq`, `X-Up-Ack`, `X-Down-Cursor`, byte-identical retry state, and at
most one active request in each direction. Therefore two Telegram MTProto sessions
may both use uplink sequence `1` and make progress independently.

When a lane has delivered and acknowledged all queued bytes after its stream closes,
the relay returns an empty response with `X-Lane-Closed: 1`; the bridge then stops
polling that lane. Recently closed lane state is retained with the stream tombstone
so a lost final uplink response can still be acknowledged idempotently. When the
tombstone is evicted, whatever the lane still held (undelivered frames and their
byte/item charges) is released, and well-formed late `DATA`, `WINDOW`, or `CLOSE`
frames for that id are acknowledged and ignored rather than failing the session;
the bridge likewise drops such frames instead of failing. Lane polls also observe
`ErrConcurrent`-free newest-poll-wins semantics per lane, exactly like `https`.

This mode mirrors Telegram's native connection allocation and removes carrier-level
head-of-line blocking between logical sessions. It assumes normal HTTP/2 service at
the public origin; HTTP/1.1 per-origin connection limits can constrain a large set
of simultaneous long polls.

### Multiplexed WebSocket

`websocket` mode performs the same HTTPS session creation, then opens:

```text
GET /api/v1/ws
Origin: https://H
Sec-WebSocket-Protocol: tproxy-v1.<session-token>
```

The relay echoes that exact subprotocol. Because the session bearer travels in a
request header here, operators must never enable header logging on the front
proxy or the relay. The relay pings the peer after every idle long-poll period and
closes the connection after two such periods without any message or pong, so a
silently dead peer does not pin the session until the listener's TCP keep-alive.
Each client WebSocket binary message is a
bounded batch of complete client-to-relay frames. Each relay binary message is a
bounded batch of complete relay-to-client frames. WebSocket ordering and reliable
delivery replace the HTTP sequence and cursor headers; the shared per-stream WINDOW
protocol remains authoritative for end-to-end backpressure. Text messages,
additional multiplexed WebSockets for the same relay session, oversized messages,
malformed frame batches, and an incorrect subprotocol are rejected.

The bridge limits its queued plus browser-buffered uplink to 32 MiB. The relay waits
up to 30 seconds for temporary backend-write backpressure to clear before closing the
carrier. A WebSocket loss closes the relay session and every logical stream; this
reference implementation does not resume a partially delivered WebSocket session.

One multiplexed WebSocket is sufficient for correctness and removes the HTTP
stop-and-wait ceiling. It does not preserve the queue and failure isolation of the
separate TCP connections Telegram normally uses for API, upload, and download
sessions.

### Stream-aware WebSocket lanes

`websocket-lanes` performs the same HTTPS session creation but opens one
same-origin WebSocket for every nonzero shared stream:

```text
GET /api/v1/ws
Origin: https://H
Sec-WebSocket-Protocol: tproxy-lane-v1.<session-token>.<decimal stream_id>
```

The relay echoes the exact subprotocol. The stream id is canonical decimal, is
never zero, and cannot be reused during the parent relay session. Only one socket
may be attached to a stream id. The first binary message must begin with `OPEN`,
and every frame sent or received on that socket has the selected stream id. There
is no lane-zero WebSocket: HTTPS bootstrap carries `HELLO` and `WELCOME`, while
WebSocket protocol ping/pong frames provide connection liveness.

Each socket has independent ordered delivery, browser buffering, relay writer,
and backend connection. Closing an established lane unexpectedly closes that one
backend stream, and the bridge returns a `CLOSE` frame to the app; other lanes and
the parent relay session remain active. A client or backend `CLOSE` completes that
lane's shutdown and then closes its socket. Failure to establish a new lane is
treated as parent-carrier failure because it normally means the relay session,
origin, or network is no longer usable. Deleting or expiring the parent session
still closes every lane. The relay closes only the affected established lane for
a text, oversized, malformed, or cross-lane client message; the bridge treats an
invalid relay message as parent-carrier failure. It retains the 32 MiB global
uplink bound and additionally limits each lane to 8 MiB and 1024 queued items.

The mode removes application-level head-of-line blocking between interactive API
and bulk media streams. It can also isolate TCP congestion and packet loss when
the WebView assigns separate network connections, but that is not guaranteed when
the engine carries several WebSockets over one HTTP/2 connection. The number of
live lane sockets is bounded by the profile and session stream limits. Operators
trade the improved isolation for additional WebSocket/TLS setup, connections, and
server resources.

`DELETE /api/v1/session` with the session bearer closes all streams and is
idempotent for a currently authenticated session. Tokens use the opaque signed
representation described above. Missing or random credentials follow the public
website; authentic but unusable credentials receive a local uncacheable 404.

## Shared frames

```text
type:u8 | stream_id:u24 | payload_length:u32 | payload
```

| Value | Name | Direction | Stream | Payload |
|---:|---|---|---:|---|
| `0x01` | `OPEN` | client → relay | nonzero | empty |
| `0x02` | `DATA` | both | nonzero | opaque, nonempty |
| `0x03` | `CLOSE` | both | nonzero | empty |
| `0x04` | `WINDOW` | both | nonzero | nonzero `u32` delta |
| `0x05` | `PING` | relay → client | zero | opaque echo token |
| `0x06` | `PONG` | client → relay | zero | exact echo token |
| `0x10` | `HELLO` | client → relay | zero | byte `01` |
| `0x11` | `WELCOME` | relay → client | zero | empty |
| `0x1f` | `BYE` | relay → client | zero | optional bounded reason |

## Client stream lifecycle

The Telegram app, not the bridge JavaScript, prepares the shared frames:

1. After the client boundary is authenticated, the client sends one
   `HELLO`. It may create streams only after receiving `WELCOME`.
2. Each MTProxy TCP connection opened by the app becomes a new, never-reused
   nonzero stream id and one `OPEN` frame.
3. Bytes already transformed for MTProxy become one or more `DATA` frames for that
   id. The client sends only within the relay-granted window.
4. Relay `DATA` is written to the corresponding local app connection. As the local
   Telegram networking engine drains those bytes, the client returns that amount as
   `WINDOW` credit.
5. EOF or failure on either side produces `CLOSE` for that stream. Other streams
   and the shared carrier continue. `CLOSE` is an abort, not a half-close:
   `DATA` still queued for that stream on either side is dropped, exactly like
   the desktop client's existing TCP path, which never half-closes an MTProto
   socket either.
6. Replacing or disabling the WEB proxy closes the carrier session and all of its
   logical streams.

The bridge batches complete client frames and delivers complete relay frames. In
`https-lanes` and `websocket-lanes` it parses only the shared header and frame
boundary needed to select a lane; it never interprets DATA, creates MTProxy
payloads, or assigns stream ids.

The implementation does not emit shared-frame PING or BYE in v1. A parent-carrier
failure closes the authenticated relay session and the bridge tells the client to
replace it. An established `websocket-lanes` socket failure closes only its stream.

The maximum payload is 1 MiB. Relay DATA chunks are at most 64 KiB. Each stream
begins with 4 MiB of credit in each direction. Client DATA consumes relay receive
credit; the relay grants WINDOW only after the bytes reach the local MTProxy TCP
socket. Backend reads consume client-granted credit and stop when it reaches zero.
One carrier body may contain at most 4096 complete frames. The default carrier body
target is 2 MiB; deployments may tune it up to the configured HTTP body limit.

Pending limits charge encoded bytes plus a conservative 256-byte cost for every
queued write or frame. Separate item limits remain authoritative even when adjacent
writes or WINDOW updates cannot be coalesced. Moving frames into the one replayable
downlink batch retains their byte and item charges until the next cursor acknowledges
that batch. Downlink DATA admission leaves room for one maximum uplink batch and
reserved byte and item headroom for WINDOW, CLOSE, and session control frames.
WINDOW grants for one stream coalesce while pending even when other streams' controls
are interleaved. Backend reads pause when the downlink DATA partition is full and
resume after a downlink acknowledgement releases capacity.

An `OPEN` creates exactly one connection to the profile's configured numeric
loopback backend. The client cannot select a destination. Stream IDs cannot be
reused during a session. Up to 4096 recently closed IDs remain as tombstones so
well-formed late DATA, WINDOW, or CLOSE frames from a close race can be ignored.
If an otherwise valid `OPEN` exceeds a per-session, profile, process-wide,
dial-in-flight, or stream-creation-rate limit, the relay returns `CLOSE` for that
stream id. It does not close the authenticated session or its other streams.

## Implementation references

- Telegram Desktop frame codec: `../tproxy/Telegram/SourceFiles/mtproto/web_proxy/web_proxy_frame.h`
- Telegram Desktop carriers: `../tproxy/Telegram/SourceFiles/mtproto/web_proxy/web_proxy_webview.cpp`
  and `../tproxy/Telegram/SourceFiles/mtproto/web_proxy/web_proxy_transport.cpp`
- Android client notes: `ANDROID.md`
- iOS client notes: `IOS.md`
- Server codec: `internal/frame/frame.go`
- Server carrier: `internal/server/server.go`
