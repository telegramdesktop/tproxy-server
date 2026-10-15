# iOS WEB proxy proof of concept

This document maps the WEB proxy carrier implemented in the official Telegram iOS
fork at `../Other/Telegram-iOS`. The proof of concept reuses iOS MtProtoKit's
existing MTProxy transform and the bridge page already used by the Android fork.
It implements the same client-neutral relay protocol as the Desktop and Android
proofs of concept.

## Chosen architecture

```text
Telegram iOS MtProtoKit
  existing MTProxy encryption and framing
        |
        | TCP to 127.0.0.1:<ephemeral>
        v
WebProxyTransport Swift sidecar
  one logical WEB stream per accepted NWConnection
        |
        | authenticated WKWebView message shim
        v
private WKWebView main frame
  https://proxy.example/?bridge=...#android=<nonce>
        |
        | same-origin HTTPS fetch long-poll
        v
tproxy-server -> stock MTProxy -> Telegram DC
```

`MTTcpConnection` already applies the MTProxy obfuscated2 transform whenever
`MTSocksProxySettings.secret` is present. The WEB adapter can therefore replace
only the configured socket address with a numeric loopback listener and preserve
the secret. The Swift and WebKit layers carry opaque, already transformed bytes;
the server still cannot select a destination or decrypt the stream. No MtProtoKit
transport rewrite is required for the first experiment.

Use Network.framework for a loopback-only `NWListener` and its accepted
`NWConnection` values. The sidecar implements the shared `HELLO`, `WELCOME`,
`OPEN`, `DATA`, `CLOSE`, `WINDOW`, `PING`, and `PONG` frames, including the
protocol limits and per-stream flow control in `PROTOCOL.md`.

## Reusing the current server bridge

No Caddy configuration, relay endpoint, or wire-protocol change is required for
the iOS proof of concept. Deploy the current server build so its bridge response
declares the hardened execution profile from `PROTOCOL.md`. The iOS client can
intentionally emulate the existing Android WebView boundary:

1. derive the same bridge capability and load the existing
   `#android=<43-character nonce>` URL;
2. inject a page-world object named `globalThis.TelegramWebProxy` before the main
   document starts;
3. translate the injected object's `postMessage` calls to a randomly named native
   `WKScriptMessageHandler`; and
4. translate native messages back to the object callback expected by the bridge.

The words `android` and `tproxy-android-init` are legacy wire names in this path,
not a platform assertion. Reusing them keeps every deployed server compatible. A
profile may select serialized HTTPS, stream-aware HTTPS lanes, multiplexed WSS,
or one WSS connection per stream; all four remain inside the server-provided page
and use the same WKWebView/native message boundary. A future protocol revision can
introduce platform-neutral names,
but that would be a
versioned cleanup rather than a prerequisite for iOS.

WebKit's script-message boundary is not the public relay protocol. The injected
shim may base64-encode a complete binary frame inside its private JSON envelope;
the remote bridge still observes the same ArrayBuffer-or-control-string contract,
and the selected carrier and frame bytes do not change.

The reference page remains functional when the `WKWebView` uses a nonpersistent
data store and the app blocks storage, subframes, media, popups, downloads,
permissions, and navigation away from the configured origin. The page still needs
its nonce-bearing inline script, exact-origin Fetch/WSS, timers, typed arrays,
same-document history replacement, and the authenticated script-message boundary.

## WKWebView boundary

Create the view with a nonpersistent `WKWebsiteDataStore` and a
`WKUserContentController`. Install a page-world `WKUserScript` at document start,
restricted to the main frame. The script defines a nonreplaceable
`TelegramWebProxy` object and uses a cryptographically random handler name and
nonce for each carrier instance.

The native handler must accept a message only when all of these still match the
active configuration:

- the sending `WKWebView` instance;
- the random handler name and nonce;
- the main frame;
- the exact `https://H` security origin; and
- the current main-frame navigation URL.

Allow only the canonical configured hostname on HTTPS port 443. Reject IP
literals, user info, redirects, TLS exceptions, subframes, wildcard origins, and
navigation away from the bridge. Remove the handler when replacing the carrier so
an old document cannot reach a new session.

Derive the capability with CryptoKit HMAC-SHA256 from the complete decoded secret,
retaining the leading `dd` byte. The historical context remains
`tdesktop-web-proxy-bridge-v1\nH`. Never log the capability, nonce, session token,
or bridge URL.

The bridge currently emits one validated shared frame per WebView binary message.
Ordinary DATA is at most 64 KiB and the protocol payload maximum is 1 MiB. Base64
copying is acceptable for a proof of concept, but memory and latency must be
measured before treating it as a production transport.

## Required WKWebView hardening

The carrier must receive its own `WKWebViewConfiguration`; never retrofit these
restrictions onto Telegram's Mini Apps, payments, 3-D Secure, Instant View embeds,
location picker, or another shared `WKProcessPool`/data store. Use
`WKWebsiteDataStore.nonPersistent()` and a fresh `WKUserContentController` for
each carrier lifetime.

Install a main-frame-only `WKUserScript` at document start, before the bridge shim
and before provider JavaScript. It must add an independent meta CSP with the exact
policy below, add
`<meta http-equiv="x-dns-prefetch-control" content="off">`, and make the unused
storage/device globals unavailable with nonreplaceable properties:

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

The provider may put its carrier implementation inline, but external code,
resources, or response data must not become usable by provider JavaScript, and it
must not create frames or workers. Response data is available only from exact
`H`; this does not guarantee that `WKWebView` emits no off-origin request, and
provider documents must not attempt one. Also shadow `localStorage`,
`sessionStorage`, IndexedDB, Cache Storage, workers, `BroadcastChannel`, browser
audio constructors, clipboard/device APIs, `window.open`, and `document.cookie`.
Make `print`, `alert`, `confirm`, and `prompt` inert too. The shims are surface
reduction; WebKit's CSP and native delegates are the security boundary. Do not add
`'unsafe-eval'`, `blob:`, `data:`, wildcard hosts, alternate ports, or an HTTP
source.

Set `mediaTypesRequiringUserActionForPlayback` to all audiovisual media, disable
AirPlay and picture-in-picture where the platform exposes those switches, keep the
view noninteractive, and deny every media-capture, orientation/motion, and other
permission callback. Return no view from popup creation, return no URLs from file
panels, cancel downloads, and never present JavaScript dialogs for the hidden
carrier.

In `WKNavigationDelegate`:

- allow only the initial main-frame canonical
  `https://H/?bridge=...#android=<nonce>` navigation;
- cancel redirects, new windows, download navigations, subframe navigations,
  user-info URLs, IP literals, alternate ports, and every other scheme or host;
- use normal system TLS validation and cancel authentication challenges rather
  than accepting an untrusted certificate; and
- treat web-content process termination or loss of responsiveness as carrier
  failure and discard the entire view/configuration.

`WKNavigationDelegate` does not observe all Fetch/WSS/subresource traffic. The
document-start CSP is therefore mandatory even when navigation checks are exact.
A compiled `WKContentRuleList` may additionally block HTTP(S) resources outside
`H`, but it does not replace CSP coverage of script-created transports. Failure to
install the document-start policy must fail closed before navigation.

There is no public per-view switch that rejects every first-party cookie while
leaving other WKWebViews alone. The nonpersistent store prevents disk persistence,
the document shim removes ordinary DOM cookie access, and the reference bridge
uses `credentials: 'omit'`; a provider can still create same-origin session state
inside its disposable store. That state can reach only the already selected proxy
origin and disappears with the carrier, so do not weaken unrelated Telegram
WebViews with global cleanup.

The provider deliberately retains unrestricted computation and same-origin
request scheduling so it can experiment with batching, padding, HTTPS lanes, or
WebSockets. Native frame/queue bounds, bridge heartbeat deadlines, renderer
termination handling, and foreground lifecycle are the resource-abuse boundary.
WebRTC is outside the reference transport and is not claimed to be reliably
disabled across all engines.

## iOS source changes

The implementation is isolated in a new Swift `WebProxyTransport` module
with `WebKit`, `Network`, and `CryptoKit` SDK dependencies. TelegramCore can depend
on that module without teaching MtProtoKit about WebKit.

- `submodules/TelegramCore/Sources/SyncCore/SyncCore_ProxySettings.swift` adds an
  explicit `.web(secret:)` connection encoded with a new `_t` value. Existing
  SOCKS5 and MTProxy records remain byte-for-byte compatible.
- `submodules/TelegramCore/Sources/Settings/ProxySettings.swift` resolves an active
  WEB relay to the sidecar's numeric loopback address, ephemeral listener port,
  and original secret. The saved public WEB endpoint remains fixed to HTTPS port
  443. Resolution must fail closed; never fall back to the public hostname as a
  direct MTProxy socket.
- Initial setup in `Network.swift` and shared live updates for authorized and
  unauthorized accounts in `Account.swift` use one idempotent process-wide
  carrier. WEB can therefore bootstrap the login network before authorization.
  Disabling or replacing WEB closes its listener, WebView, HTTP session, and all
  logical streams.
- `ProxyServerSettingsController.swift` adds a WEB mode, fixes its public port to
  443, and validates a canonical DNS hostname, an optional base path, and a
  supported MTProxy secret. Link parsing applies the marked-secret rule below.

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
- `ProxyListSettingsController.swift`, `DataAndStorageSettingsController.swift`,
  Settings search, peer-info settings, and QR/preview switches learn the explicit
  type.
- `ProxyServersStatuses.swift` does not ping inactive WEB rows. They display
  `Not tested`; only the active row follows the real account connection state.
- Calls remain SOCKS5-only in the WKWebView-only POC. When a relay profile
  advertises `X-Call-TCP: 1` (`"calls_tcp": true`), a native CallKit-scoped sidecar
  can obtain an independent session token from `GET /?bridge=<capability>` and open
  `OPEN_TCP` (`0x07`) streams directly so active calls survive screen-off and
  backgrounding.
- `ProxyServerPreviewScreen.swift` must not compare a WEB connection's reported
  loopback address with its saved public hostname. Its WEB preview omits the
  implicit HTTPS port and inactive status check; Connect waits for the account
  network to report online through the loopback adapter.

The fork implements `tg://webproxy` handling across `UrlHandling`,
`OpenResolvedUrl`, sharing, clipboard parsing, and QR code generation. Its isolated
local app registers `tproxyweb` instead of competing with the installed Telegram
app for `tg`; direct device tests replace only the scheme. The public `t.me`
frontend does not currently register a WEB proxy route.

Official Telegram iOS exposes proxy settings before login only from its
network-timeout alert; it has no persistent authorization-screen proxy indicator.
A production WEB UI should match Android's pre-login proxy icon and connection
state.

TelegramCore is also linked into app extensions. The transport rejects startup
when the main bundle path ends in `.appex`, and the isolated POC build disables
extensions entirely.

## Lifecycle limitation

This is a foreground-only POC. iOS normally suspends an app shortly after it enters
the background, and a background task grants only limited completion time. The
carrier must close or become unavailable on background and recreate after the app
returns to the foreground. It must not claim continuous background delivery.

The implementation attaches a strongly retained, transparent, noninteractive
one-pixel `WKWebView` to the app window while WEB is active. Do not use background
audio, location, VoIP, or another unrelated background mode to keep it alive.

One process-wide carrier should serve all accounts. Account-specific observers may
request the same configuration, but carrier start/stop and listener allocation
must remain idempotent. A conflicting WEB configuration should replace the old one
and force all account networks to reconnect.

## Identity and diagnostics

MtProtoKit currently reports `MTSocksProxySettings.ip` as the active proxy address.
For the POC that value is `127.0.0.1`, while the saved relay is the public hostname.
The preview/status UI must account for this or it can wait forever for an address
match. Do not display or share the loopback endpoint.

Before production, add an explicit proxy kind plus separate connect and display
addresses below TelegramCore. This also prevents the loopback address from being
used for proxy-sponsored-channel identity or diagnostics. New carrier diagnostics
may expose coarse states, stream counts, and aggregate byte counts, never
endpoints, capabilities, secrets, bearer tokens, bridge URLs, or message content.

## Build and device test

Follow the local checkout's `TPROXYWEB.md` for the prepared machine state, signing
inputs, exact project-generation command, and device checklist. The first build
should use Xcode-managed signing and disabled extensions so notification, widget,
share, broadcast, and intent profiles are not prerequisites for the transport
experiment.

Test at minimum:

1. existing SOCKS5 and MTProxy records still decode, edit, share, and connect;
2. plain 16-byte and `dd` secrets both connect through WEB;
3. messages and large upload/download traffic respect flow control;
4. disable, edit, relaunch, foreground/background, Wi-Fi/cellular changes, and a
   terminated WebKit content process reconnect cleanly;
5. wrong secret, bad certificate, redirect, invalid hostname, malformed frame,
   queue overflow, and server loss fail closed; and
6. the public TLS connection originates in WebKit while the stock MTProxy backend
   still receives valid transformed traffic.

Useful Apple references are
[WKScriptMessageHandler](https://developer.apple.com/documentation/webkit/wkscriptmessagehandler),
[WKUserScript main-frame restriction](https://developer.apple.com/documentation/webkit/wkuserscript/1537856-formainframeonly),
[Network.framework local endpoints](https://developer.apple.com/documentation/network/nwparameters/requiredlocalendpoint),
and [background execution limits](https://developer.apple.com/documentation/uikit/extending-your-app-s-background-execution-time).

## Production follow-up

Deterministic tests cover capability derivation, hostname/secret validation, frame
parsing, malformed input, and persistence compatibility. A production pass should
add a local WebKit bridge fixture, state-machine and queue-bound fault injection,
and WebKit process termination coverage. It should also measure memory, CPU,
base64-copy overhead, reconnect latency, foreground endurance, and multi-account
behavior on multiple iOS releases before considering a broader rollout.
