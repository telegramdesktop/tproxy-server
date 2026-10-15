# Android WEB proxy proof of concept

This document maps the WEB proxy carrier onto the official Telegram Android
source at `../Other/Telegram-Android`. The proof of concept deliberately reuses
Android tgnet's existing MTProxy transform and implements the shared, client-neutral
relay protocol in `PROTOCOL.md`.

## Chosen architecture

```text
Telegram Android tgnet
  existing MTProxy encryption and framing
        |
        | TCP to 127.0.0.1:<ephemeral>
        v
WebProxyTransport Java sidecar
  one logical WEB stream per accepted socket
        |
        | origin-scoped binary WebMessage frames
        v
private Android WebView main frame
  https://proxy.example/?bridge=...#android=<nonce>
        |
        | same-origin HTTPS fetch long-poll
        v
tproxy-server -> stock MTProxy -> Telegram DC
```

The important shortcut is the loopback listener. When tgnet sees a proxy address,
port, and MTProxy secret, it already performs the exact obfuscated2/MTProxy
transformation expected by the stock MTProxy backend. Pointing that connection at
the Java sidecar means the Java and WebView layers see only transformed bytes. No
JNI transport rewrite is required for the first Android experiment.

The Java sidecar implements the shared `OPEN`, `DATA`, `CLOSE`, `WINDOW`, `HELLO`,
`WELCOME`, `PING`, and `PONG` frames. It maintains the 4 MiB per-direction stream
windows, limits DATA chunks to 64 KiB, bounds its outbound queue, and closes all
local sockets when the carrier is replaced.

## Why an origin-scoped WebMessage listener

The bridge is a remote HTTPS document. `addJavascriptInterface` would inject a
Java object into every frame and would not tell Android which origin invoked it.
The proof of concept instead uses AndroidX WebKit's
`WebViewCompat.addWebMessageListener` with one exact `https://hostname` allow rule.
The listener accepts only main-frame messages from that exact origin and requires
a random 32-byte nonce delivered in the URL fragment. The fragment is never sent
to the server and the bridge removes it immediately.

The installed Android System WebView must expose `WEB_MESSAGE_LISTENER`,
`WEB_MESSAGE_ARRAY_BUFFER`, and `DOCUMENT_START_SCRIPT`. AndroidX WebKit 1.14.0
was chosen because it exposes those feature checks while retaining the upstream
app's API 21 minimum. A missing feature fails closed by pointing tgnet at an
unused loopback port; it never falls back to a direct Telegram connection.

The transport is foreground-scoped. It does not request foreground-service status
or elevated renderer priority. If the renderer dies while Telegram is backgrounded,
the sidecar closes its logical streams and defers WebView recreation until the next
foreground transition. A WebView that survives may remain connected, but background
continuity is neither required nor promised.

The HTTPS bridge splits downlink carrier batches into individual validated frames
before crossing WebView IPC. This keeps ordinary messages around 64 KiB and avoids
passing a full 2 MiB long-poll response through a single WebMessage.

## Required WebView hardening

Apply these settings only to the private carrier WebView. Telegram's Mini Apps,
payments, Instant View embeds, location picker, and other browser surfaces need
their existing profiles and capabilities. In particular, do not change the
process-global cookie or service-worker policy to harden this one view.

Before the first `loadUrl`, use
`WebViewCompat.addDocumentStartJavaScript` with the same exact-origin rule as the
message listener. The script must run before provider JavaScript and install a
second CSP independent of the response headers. It should also install
`<meta http-equiv="x-dns-prefetch-control" content="off">` before provider
markup is parsed:

```text
default-src 'none';
base-uri 'none';
child-src 'none';
connect-src https://H wss://H;
font-src 'none';
form-action 'none';
frame-src 'none';
img-src 'none';
manifest-src 'none';
media-src 'none';
object-src 'none';
script-src 'unsafe-inline';
style-src 'none';
worker-src 'none'
```

`'unsafe-inline'` is intentional: the provider controls the self-contained carrier
script. Omitting `'unsafe-eval'`, every resource source, and every origin except
exact HTTPS/WSS `H` still prevents external code or response data, subframes,
workers, and media from becoming usable by provider JavaScript. This does not
guarantee that the WebView emits no off-origin request, and provider documents
must not attempt one. The same document-start script should replace
`localStorage`, `sessionStorage`, IndexedDB, Cache Storage, workers,
`BroadcastChannel`, browser audio constructors, clipboard/device APIs,
`window.open`, and `document.cookie` with nonreplaceable unavailable shims.
Replace `print`, `alert`, `confirm`, and `prompt` with inert functions as well.
These shims reduce exposed surface; the CSP and native policy remain the security
boundary.

Configure the carrier instance as follows:

- keep JavaScript enabled, but disable DOM storage and database storage;
- use `LOAD_NO_CACHE`, clear only carrier-owned cache state, and never share a
  carrier data directory/profile with an ordinary Telegram WebView;
- disable file and content access, mixed content, image loading, geolocation,
  automatic windows, form-data saving, and multiple-window support;
- require a real user gesture for media playback and leave the hidden view without
  any user interaction path;
- keep Safe Browsing enabled and never install a certificate-error bypass;
- deny every `WebChromeClient` permission, geolocation, file chooser, popup, and
  window-creation callback; ignore every download request; and
- accept main-frame navigation only to the one canonical nonce-bearing bridge URL.
  Reject redirects, subframes, IP literals, user info, non-443 ports, HTTP, and
  every new-window navigation.

`shouldInterceptRequest` should return an empty failure response for HTTP(S)
requests whose scheme, canonical host, or effective port differs from
`https://H:443`. It is defense in depth, not the only allowlist: Android request
interception does not cover every browser transport, so the injected CSP is also
required to constrain WSS and future script-created connections. Continue to
validate the main-frame origin, active WebView, nonce, and current navigation at
the WebMessage boundary.

Baseline Android's `CookieManager.setAcceptCookie` is process-global. Do not call
it if the process contains other Telegram WebViews. Prefer a disposable isolated
WebView profile when the installed provider exposes one; otherwise disable DOM
cookie access in the document-start script, keep the reference bridge's
`credentials: 'omit'`, and never reuse carrier browser state. This prevents
persistent tracking without silently breaking Mini Apps or payments. Destroy the
view, remove its message listener and scripts, and delete only its disposable
profile when the carrier stops.

The provider can still spend CPU or memory and can generate arbitrary amounts of
same-origin carrier traffic; those are inherent in granting it transport-shaping
freedom. Treat an unresponsive/terminated renderer, bridge heartbeat timeout,
native queue limit, or frame-size violation as carrier failure, destroy the whole
private profile, and recreate it only while Telegram is foregrounded. WebRTC is
not part of the reference transport and is not claimed to be reliably disabled on
all client engines.

## Server changes

The relay endpoints, session model, authentication, limits, and Caddy deployment do
not change. The generated bridge page now has two mutually exclusive initialization
paths:

1. the loopback-parent `MessageChannel` used by optional browser carriers; or
2. the exact-origin `TelegramWebProxy` object injected by AndroidX WebKit when the
   URL contains a canonical `#android=<43-character nonce>` fragment.

Control objects are JSON strings at the Android boundary. Binary messages are one
complete shared frame. See `PROTOCOL.md` for the normative boundary. Existing
loopback-parent bridge URLs have no WebView fragment and continue down the original
path.

No configuration, deployment, or Caddy change is required. A server must be updated
to a build containing the Android bridge-page extension before the Android client
can connect. The current bridge response also declares the hardened execution
profile from `PROTOCOL.md`: all subresources, workers, media, frames, and the
enumerated unused browser permissions are denied, and the page has no storage
dependency. Inline nonce script execution, same-origin Fetch/WSS, and the
authenticated WebMessage boundary remain available. The selected carrier mode is
implemented by the server-provided page and does not change the Android boundary.

## Android source changes

The proof of concept touches these areas in the cloned upstream source:

- `TMessagesProj/src/main/java/org/telegram/messenger/WebProxyTransport.java`
  owns the loopback listener, stream mux, hidden WebView, capability derivation,
  origin checks, and reconnect lifecycle.
- `SharedConfig.ProxyInfo` has an explicit SOCKS5 / MTProto / WEB type. Proxy-list
  schema v3 appends the type after the v2 record fields, while v2 and legacy lists
  retain their inferred types.
- `ConnectionsManager` converts an enabled WEB hostname into the sidecar's numeric
  loopback address while preserving the user's MTProxy secret for native tgnet.
- `ProxySettingsActivity` exposes a third `WEB Proxy` choice, fixes the effective
  port to 443, validates a canonical DNS hostname and 16-byte or `dd` secret, and
  shares the dedicated `t.me/webproxy` link type.
- `ProxyListActivity` stores and displays the explicit type. Availability checks and
  automatic rotation skip WEB entries because checking one would activate a
  process-wide WebView carrier.
- `androidx.webkit:webkit:1.14.0` provides the exact-origin binary message API.

## Proxy links

The canonical share link is:

```text
https://t.me/webproxy?server=proxy.example.com&secret=000102030405060708090a0b0c0d0e0f
```

`server` is the canonical DNS hostname and `secret` is the same client-facing
16-byte or `dd`-prefixed MTProxy secret used by the relay profile. The link has no
port because WEB always uses HTTPS on 443, and it has no username or password.
Clients also accept `tg://webproxy?server=...&secret=...`; `host` is accepted as a
legacy input alias, but generated links always use `server`.

The link secret is derived from the MTProxy secret in `profiles.json`:

```text
root deployment : secret          -> the plain hex, unchanged
base path       : 0x70 || secret  -> unpadded base64url
```

```bash
# the exact derivation deploy/install.sh performs
{ printf '\x70'; printf "$(printf %s "$secret" | sed 's/../\\x&/g')"; } \
  | base64 | tr '+/' '-_' | tr -d '=\n'
# 8561944064fc730cbfa4473562d8ec59 -> cIVhlEBk_HMMv6RHNWLY7Fk
```

A client decodes it by the inverse rule: base64url-decode, and if the result is
at least 17 bytes and begins with `0x70`, strip that byte and use the rest as the
MTProxy secret; otherwise use the value as it stands. This is unambiguous because
a canonical secret is 16 bytes, 17 beginning with `0xDD`, or 21+ beginning with
`0xEE`. A link that carries a base path **must** use the marked form — an unmarked
secret there is rejected, so that no link exists which an older client would
silently accept as a pathless proxy on an empty host. Never use `0xDD` as the
marker: an older parser reads a 17-byte secret beginning with it as an ordinary
padded secret and accepts the link.

The Android client validates and canonicalizes both fields before treating the URL
as a proxy link. Its confirmation sheet displays only the address and secret, with
no independent status check, and starts the foreground WebView carrier only after
the user chooses to connect. `tproxy-server` never handles the `t.me` URL itself,
so this convention requires no relay endpoint or deployment change.

The public `t.me` web frontend does not currently register `/webproxy`; if a
browser handles the URL, it falls back to the `@webproxy` username. The POC link is
therefore guaranteed only when TproxyWeb handles it internally or Android routes
the HTTPS intent directly to TproxyWeb. Use the `tg://webproxy` form for direct
cross-app testing and choose TproxyWeb when another Telegram client is installed.
A production-wide HTTPS link requires Telegram to register the route on `t.me`.

## Build

The upstream checkout requires its three shallow submodules plus JDK 17, Android
SDK/platform 35, build-tools 35.0.0, NDK 27.2.12479018, and CMake 3.10.2. On this
Mac the SDK root is `/opt/homebrew/share/android-commandlinetools` and the JDK is
`/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home`.

```bash
git submodule update --init --recursive --depth=1

JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home \
ANDROID_HOME=/opt/homebrew/share/android-commandlinetools \
ANDROID_SDK_ROOT=/opt/homebrew/share/android-commandlinetools \
./gradlew :TMessagesProj_App:assembleAfatDebug
```

The expected APK is
`TMessagesProj_App/build/outputs/apk/afat/debug/app.apk`. The upstream repository
contains dummy reproducible-build credentials; the result is for local testing,
not publication.

## Test sequence

1. Deploy the updated relay and confirm an existing WEB client still connects.
2. Install the debug APK and ensure Android System WebView is current.
3. Add a WEB proxy with the deployed hostname and the same MTProxy secret.
4. Enable it in the foreground. The proxy row should move from connecting to the
   normal connected state without opening a browser tab.
5. Confirm the server sees one session and multiple logical streams, and confirm the
   public TLS peer is Android System WebView rather than tgnet.
6. Send messages, download and upload a large file, switch networks, disable and
   re-enable the proxy, edit the secret, and restart the app.
7. Background the app, terminate the WebView renderer, and return to the app. The
   carrier should be recreated only after Telegram becomes active and reconnect
   without opening a browser tab.
8. Repeat with a plain 16-byte secret and a `dd` random-padding secret.
9. Verify SOCKS5 and ordinary MTProto entries still load from old proxy-list data,
   connect, share, and rotate as before.

Useful negative tests are an invalid certificate, redirected hostname, wrong
secret, missing WebMessage ArrayBuffer support, WebView renderer termination, a
malformed frame, a queue overflow, and app background/foreground transitions.

## Known proof-of-concept limits

- The WebView is deliberately private, unattached, and foreground-scoped. Android
  may throttle or kill it in the background; this is accepted behavior. No
  foreground service, background keepalive, or renderer-retention workaround is
  required for this feature.
- There is no independent WEB proxy ping. Non-active WEB rows display `Not tested`;
  the real connection state is learned only after activation. Automatic or
  sequential list-page tests would churn the process-wide WebView carrier and are
  intentionally unsupported.
- WEB entries are excluded from proxy rotation and calls in the WebView-only POC.
  One process-wide WebView carrier serves all Telegram accounts. When a relay
  profile advertises `X-Call-TCP: 1` (`"calls_tcp": true`), background-safe voice
  calls can use a native Dual-Session Call Sidecar that fetches
  `GET /?bridge=<capability>` directly to obtain an independent session token and
  opens `OPEN_TCP` (`0x07`) streams without depending on the foreground WebView.
- The historical capability context remains
  `tdesktop-web-proxy-bridge-v1` for wire compatibility. Renaming it would rotate
  every deployed capability and requires an explicit protocol version.
- Native tgnet still identifies its configured proxy internally as the loopback
  endpoint. A production patch should add an explicit proxy kind and separate the
  user-visible/reportable relay hostname from the low-level connect endpoint,
  especially before relying on proxy-sponsored-channel metadata.
- The POC has no UI diagnostics for a missing/outdated System WebView. Production UI
  should surface unsupported, loading, connected, reconnecting, and renderer-gone
  states without exposing the bridge capability or Android nonce.

## Production follow-up

Before proposing this upstream, add unit tests for capability/hostname/secret
vectors and the Java frame codec, instrumentation tests with a deterministic local
bridge, foreground-resume and network-change handling, metrics that do not log
capabilities, and a native proxy-kind field that prevents loopback details from
leaking into proxy metadata. Then run the foreground reliability, memory,
backpressure, certificate, and resume matrix on representative Android/WebView
versions.
