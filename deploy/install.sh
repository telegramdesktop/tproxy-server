#!/usr/bin/env bash
set -euo pipefail
umask 077

hostname=
base_path=
secret=
email=
site_dir=
site_upstream=
static_routes=exact
mtproxy_workers=1
mtproxy_max_connections=4096

usage() {
	echo "usage: sudo ./deploy/install.sh --hostname proxy.example.com --email admin@example.com [--site-dir DIR | --site-upstream URL] [--base-path SLUG|none] [--static-routes exact|legacy] [--secret 32-or-34-hex] [--mtproxy-workers 1] [--mtproxy-max-connections 4096]" >&2
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--hostname) hostname="${2:-}"; shift 2 ;;
		--base-path) base_path="${2:-}"; shift 2 ;;
		--secret) secret="${2:-}"; shift 2 ;;
		--email) email="${2:-}"; shift 2 ;;
		--site-dir) site_dir="${2:-}"; shift 2 ;;
		--site-upstream) site_upstream="${2:-}"; shift 2 ;;
		--static-routes) static_routes="${2:-}"; shift 2 ;;
		--mtproxy-workers) mtproxy_workers="${2:-}"; shift 2 ;;
		--mtproxy-max-connections) mtproxy_max_connections="${2:-}"; shift 2 ;;
		*) usage; exit 2 ;;
	esac
done

if [[ "${EUID}" -ne 0 ]]; then
	echo "run this installer as root" >&2
	exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
	echo "the stock official MTProxy build requires an x86_64 server" >&2
	exit 1
fi
if [[ -z "$secret" ]]; then
	read -r -s -p "WEB proxy secret (32 hex, optionally prefixed with dd): " secret
	echo
fi
# A new deployment gets a fresh 128-bit slug, so its carrier stays off
# well-known root paths by default. "none" selects the host root, which is what
# every installation made before base paths existed serves.
if [[ "$base_path" == "none" ]]; then
	base_path=
elif [[ -z "$base_path" ]]; then
	if [[ -f /etc/tproxy-server/config.json ]]; then
		# A reinstall keeps whatever this host already serves. Rotating the
		# prefix would invalidate every client link and drop live sessions, and
		# a config predating the key is serving the root.
		base_path="$(sed -n 's/.*"base_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' /etc/tproxy-server/config.json | head -n1)"
	elif ! command -v base32 >/dev/null; then
		echo "base32 (coreutils) is required to generate a base path; pass --base-path explicitly" >&2
		exit 1
	else
		base_path="$(head -c 10 /dev/urandom | base32 | tr 'A-Z' 'a-z')"
	fi
fi
if [[ -n "$base_path" ]] && ! [[ "$base_path" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9][A-Za-z0-9_-]*)*$ ]]; then
	echo "base path segments must match [A-Za-z0-9][A-Za-z0-9_-]* joined by /" >&2
	usage
	exit 1
fi
if [[ ${#base_path} -gt 128 ]]; then
	echo "base path must be at most 128 characters" >&2
	exit 1
fi
if [[ ! "$hostname" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]] || [[ "$hostname" != *.* ]]; then
	echo "hostname must be a lowercase ASCII DNS hostname" >&2
	exit 2
fi
if [[ ! "$secret" =~ ^([0-9a-f]{32}|dd[0-9a-f]{32})$ ]]; then
	echo "secret must be 32 lowercase hex characters, optionally prefixed with dd" >&2
	exit 2
fi
if [[ ! "$email" =~ ^[A-Za-z0-9._+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]; then
	echo "a valid ACME contact email is required" >&2
	exit 2
fi
if [[ ! "$mtproxy_workers" =~ ^[1-9][0-9]*$ ]] || ((mtproxy_workers > 256)); then
	echo "mtproxy workers must be between 1 and 256" >&2
	exit 2
fi
if [[ ! "$mtproxy_max_connections" =~ ^[1-9][0-9]*$ ]]; then
	echo "mtproxy max connections must be positive" >&2
	exit 2
fi

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$static_routes" != exact && "$static_routes" != legacy ]]; then
	echo "--static-routes must be exact or legacy" >&2
	exit 2
fi
if [[ -n "$site_dir" ]] && [[ -n "$site_upstream" ]]; then
	echo "--site-dir and --site-upstream are mutually exclusive" >&2
	exit 2
fi
if [[ -n "$site_dir" ]]; then
	if [[ ! -d "$site_dir" ]]; then
		echo "site directory does not exist: $site_dir" >&2
		exit 2
	fi
	site_dir="$(cd "$site_dir" && pwd -P)"
	if [[ ! -f "$site_dir/index.html" ]] || [[ ! -r "$site_dir/index.html" ]]; then
		echo "site directory must contain a readable regular index.html" >&2
		exit 2
	fi
elif [[ -n "$site_upstream" ]]; then
	if [[ ! "$site_upstream" =~ ^http://(127\.[0-9]+\.[0-9]+\.[0-9]+|\[::1\]):[1-9][0-9]{0,4}$ ]]; then
		echo "site upstream must be http:// followed by a numeric loopback address and port" >&2
		exit 2
	fi
elif [[ ! -f /srv/tproxy-site/index.html ]]; then
	echo "a fresh installation requires --site-dir DIR or --site-upstream URL" >&2
	echo "see PUBLIC_SITE.md for the site package contract" >&2
	exit 2
fi
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl nftables

caddy_version=2.11.4
caddy_checksum=8220d1f013b6f27510247b2360c9e0ca9f018feebd82515f07635318b34ff9777ccc8fd0b6e6f2486ce3a33fe389fbb7db12d05baa474f4587509fb4f5ebf1c9
caddy_archive="$(mktemp /tmp/caddy-linux-amd64.XXXXXX.tar.gz)"
caddy_directory="$(mktemp -d /tmp/caddy-linux-amd64.XXXXXX)"
trap 'rm -f "$caddy_archive"; rm -rf "$caddy_directory"' EXIT
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
	--output "$caddy_archive" "https://github.com/caddyserver/caddy/releases/download/v${caddy_version}/caddy_${caddy_version}_linux_amd64.tar.gz"
test "$(sha512sum "$caddy_archive" | awk '{print $1}')" = "$caddy_checksum"
tar -C "$caddy_directory" -xzf "$caddy_archive"
install -m 0755 "$caddy_directory/caddy" /usr/local/bin/caddy
rm -f "$caddy_archive"
rm -rf "$caddy_directory"
trap - EXIT

if ! id caddy >/dev/null 2>&1; then
	useradd --system --home /var/lib/caddy --shell /usr/sbin/nologin caddy
fi
install -d -o root -g caddy -m 0750 /etc/caddy
install -d -o caddy -g caddy -m 0750 /var/lib/caddy

"$repository/deploy/install-mtproxy.sh"

if ! id tproxy >/dev/null 2>&1; then
	useradd --system --home /nonexistent --shell /usr/sbin/nologin tproxy
fi

go_binary=
if command -v go >/dev/null 2>&1; then
	go_minor="$(go env GOVERSION | sed -E 's/^go1\.([0-9]+).*/\1/')"
	if [[ "$go_minor" =~ ^[0-9]+$ ]] && [[ "$go_minor" -ge 20 ]]; then
		go_binary="$(command -v go)"
	fi
fi
if [[ -z "$go_binary" ]]; then
	go_version=1.26.5
	go_checksum=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
	go_archive="$(mktemp /tmp/go-linux-amd64.XXXXXX.tar.gz)"
	go_directory="$(mktemp -d /tmp/go-linux-amd64.XXXXXX)"
	trap 'rm -f "$go_archive"; rm -rf "$go_directory"' EXIT
	curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
		--output "$go_archive" "https://go.dev/dl/go${go_version}.linux-amd64.tar.gz"
	test "$(sha256sum "$go_archive" | awk '{print $1}')" = "$go_checksum"
	tar -C "$go_directory" -xzf "$go_archive"
	if [[ -e "/opt/go${go_version}" ]]; then
		rm -rf "$go_directory/go"
	else
		mv "$go_directory/go" "/opt/go${go_version}"
	fi
	rm -f "$go_archive"
	rm -rf "$go_directory"
	trap - EXIT
	go_binary="/opt/go${go_version}/bin/go"
fi

(cd "$repository" && "$go_binary" test ./...)
(cd "$repository" && "$go_binary" build -trimpath -ldflags='-s -w' -o /usr/local/bin/tproxy-server ./cmd/tproxy-server)
chown root:root /usr/local/bin/tproxy-server
chmod 0755 /usr/local/bin/tproxy-server

install -d -o root -g root -m 0755 /srv/tproxy-site
if [[ -n "$site_dir" ]] && [[ ! -e /srv/tproxy-site/index.html ]]; then
	if [[ -n "$(find /srv/tproxy-site -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
		echo "/srv/tproxy-site is not empty but has no index.html; move it aside or complete it" >&2
		exit 1
	fi
	cp -a "$site_dir/." /srv/tproxy-site/
	chown -R root:root /srv/tproxy-site
elif [[ -n "$site_dir" ]] && [[ "$site_dir" != "$(cd /srv/tproxy-site && pwd -P)" ]]; then
	echo "Preserving the existing /srv/tproxy-site; update it separately and restart tproxy-server"
fi

if [[ -n "$site_upstream" ]]; then
	public_source="  \"public_upstream\": \"$site_upstream\","
else
	public_source='  "public_dir": "/srv/tproxy-site",'
fi

install -d -o root -g tproxy -m 0750 /etc/tproxy-server
bash "$repository/deploy/ensure-token-key.sh"
cat > /etc/tproxy-server/config.json <<EOF
{
  "public_hostname": "$hostname",
  "base_path": "$base_path",
  "listen": "127.0.0.1:8080",
  "admin_listen": "127.0.0.1:8081",
$public_source
  "static_routes": "$static_routes",
  "profiles_file": "/run/credentials/tproxy-server.service/profiles.json"
}
EOF
# Omitting carrier_mode selects "https". On TSPU/DPI-filtered networks where
# RFC 6455 WebSocket upgrades (101 Switching Protocols) are throttled after
# ~2 MiB, set "carrier_mode":"https-lanes" for per-stream HTTP/2 isolation.
cat > /etc/tproxy-server/profiles.json <<EOF
{"profiles":[{"name":"default","secret":"$secret","backend":"127.0.0.1:2398"}]}
EOF
chown root:tproxy /etc/tproxy-server/config.json /etc/tproxy-server/profiles.json
chmod 0640 /etc/tproxy-server/config.json
chmod 0400 /etc/tproxy-server/profiles.json

backend_secret="$secret"
if [[ "$backend_secret" == dd* ]] && [[ ${#backend_secret} -eq 34 ]]; then
	backend_secret="${backend_secret:2}"
fi
# MTProxy derives the AES keys for its RPC session with a Telegram middle-end
# from its own source address (net/net-tcp-rpc-client.c passes
# nat_translate_ip(c->our_ip) and c->our_port into aes_create_keys). When the
# host is behind 1:1 NAT - EC2, GCE, a container bridge - the middle-end derives
# its half from the post-NAT public address instead, the two disagree, and every
# middle-end connection is dropped right after the handshake with "Disconnected
# from RPC Middle-End". Clients still complete the obfuscated2 handshake and are
# then answered by nobody, so the proxy looks reachable and every stream stalls.
mtproxy_nat_args=
# The address MTProxy binds its outbound middle-end sockets to.
local_address="$(ip -4 route get 149.154.175.50 2>/dev/null |
	sed -n 's/.*[[:space:]]src[[:space:]]\+\([0-9.]\+\).*/\1/p' | head -n 1)"
# The address a middle-end sees those sockets arrive from. An echo service
# measures the egress address directly, which is what MTProxy has to hash; the
# hostname's own A record is the offline fallback.
public_address=
for probe in https://api.ipify.org https://ifconfig.co/ip https://icanhazip.com; do
	candidate="$(curl --fail --silent --show-error --location --ipv4 --max-time 15 \
		--proto '=https' --proto-redir '=https' --tlsv1.2 "$probe" 2>/dev/null |
		tr -d '[:space:]')" || continue
	if [[ "$candidate" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		public_address="$candidate"
		break
	fi
done
if [[ -z "$public_address" ]]; then
	public_address="$(getent ahostsv4 "$hostname" 2>/dev/null | awk 'NR==1 {print $1}')"
fi
if [[ -n "$local_address" ]] && [[ -n "$public_address" ]] &&
	[[ "$local_address" != "$public_address" ]]; then
	mtproxy_nat_args="--nat-info $local_address:$public_address"
	echo "MTProxy is behind NAT ($local_address -> $public_address), using $mtproxy_nat_args"
elif [[ "$local_address" =~ ^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.) ]] &&
	[[ -z "$mtproxy_nat_args" ]]; then
	echo "warning: $local_address is private and the public address could not be" >&2
	echo "determined; set MTPROXY_NAT_ARGS=--nat-info $local_address:<public-ip> in" >&2
	echo "/etc/mtproxy/mtproxy.env or MTProxy will accept clients and answer none" >&2
fi

cat > /etc/mtproxy/mtproxy.env <<EOF
MTPROXY_SECRET=$backend_secret
MTPROXY_WORKERS=$mtproxy_workers
MTPROXY_MAX_CONNECTIONS=$mtproxy_max_connections
MTPROXY_NAT_ARGS=$mtproxy_nat_args
EOF
chown root:mtproxy /etc/mtproxy/mtproxy.env
chmod 0640 /etc/mtproxy/mtproxy.env

install -m 0644 "$repository/deploy/Caddyfile" /etc/caddy/Caddyfile.tproxy
if [[ -e /etc/caddy/Caddyfile ]] && ! cmp -s /etc/caddy/Caddyfile "$repository/deploy/Caddyfile"; then
	cp -a /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.before-tproxy.$(date +%Y%m%d%H%M%S)"
fi
install -m 0644 "$repository/deploy/Caddyfile" /etc/caddy/Caddyfile
if [[ -e /etc/systemd/system/caddy.service ]] && ! cmp -s /etc/systemd/system/caddy.service "$repository/deploy/caddy.service"; then
	cp -a /etc/systemd/system/caddy.service "/etc/systemd/system/caddy.service.before-tproxy.$(date +%Y%m%d%H%M%S)"
fi
install -m 0644 "$repository/deploy/caddy.service" /etc/systemd/system/caddy.service
install -d -m 0755 /etc/systemd/system/caddy.service.d
cat > /etc/systemd/system/caddy.service.d/tproxy.conf <<EOF
[Service]
Environment=TPROXY_HOSTNAME=$hostname
Environment=TPROXY_SITE_ROOT=/srv/tproxy-site
Environment=ACME_EMAIL=$email
EOF

install -m 0644 "$repository/deploy/tproxy-server.service" /etc/systemd/system/tproxy-server.service
install -m 0644 "$repository/deploy/mtproxy.service" /etc/systemd/system/mtproxy.service
install -m 0644 "$repository/deploy/tproxy-firewall.service" /etc/systemd/system/tproxy-firewall.service
install -m 0644 "$repository/deploy/refresh-mtproxy-config.service" /etc/systemd/system/refresh-mtproxy-config.service
install -m 0644 "$repository/deploy/refresh-mtproxy-config.timer" /etc/systemd/system/refresh-mtproxy-config.timer
install -m 0644 "$repository/deploy/firewall.nft" /etc/tproxy-server/firewall.nft
install -m 0755 "$repository/deploy/refresh-mtproxy-config.sh" /usr/local/sbin/refresh-mtproxy-config

/usr/local/bin/tproxy-server -config /etc/tproxy-server/config.json \
	-profiles-file /etc/tproxy-server/profiles.json -check
TPROXY_HOSTNAME="$hostname" TPROXY_SITE_ROOT=/srv/tproxy-site ACME_EMAIL="$email" \
	/usr/local/bin/caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl daemon-reload
systemctl enable --now tproxy-firewall.service
systemctl enable --now mtproxy.service
systemctl restart mtproxy.service
systemctl enable --now tproxy-server.service
systemctl enable --now refresh-mtproxy-config.timer
systemctl enable --now caddy.service
systemctl restart caddy.service

relay_ready=
for ((attempt = 0; attempt != 20; ++attempt)); do
	if curl --fail --silent --output /dev/null \
			http://127.0.0.1:8081/readyz; then
		relay_ready=1
		break
	fi
	sleep 1
done
if [[ -z "$relay_ready" ]]; then
	echo "tproxy-server did not become ready" >&2
	exit 1
fi

# The client-facing proxy secret. Under a base path it is base64url of the marker
# byte 0x70 followed by the raw secret, so a client without base path support
# reports an unsupported proxy type and asks the user to update, instead of
# accepting a pathless proxy on an empty host. A root deployment keeps the plain
# secret and keeps working in those clients. Never capture the raw secret in a
# variable: it can contain NUL bytes, which command substitution drops.
web_proxy_secret() {
	if [[ -z "$base_path" ]]; then
		printf %s "$secret"
		return
	fi
	{
		printf '\x70'
		printf "$(printf %s "$secret" | sed 's/../\\x&/g')"
	} | base64 | tr '+/' '-_' | tr -d '=\n'
}

client_address="$hostname"
if [[ -n "$base_path" ]]; then
	client_address="$hostname/$base_path"
fi
proxy_secret="$(web_proxy_secret)"

# The hex secret is the value shared with MTProxy; a link under a base path must
# not carry it. Label the two apart so neither is pasted in the other's place.
echo
echo "Installed for https://$hostname/$base_path"
echo "Internal mtproxy secret: $secret"
echo "Proxy server:            $client_address"
echo "Proxy secret:            $proxy_secret"
echo "Proxy link:              https://t.me/webproxy?server=${client_address//\//%2F}&secret=$proxy_secret"
echo "Check: systemctl --no-pager --full status caddy mtproxy tproxy-server"
echo "Check: curl --fail https://$hostname/"
echo "Check: curl --fail http://127.0.0.1:8081/readyz"
