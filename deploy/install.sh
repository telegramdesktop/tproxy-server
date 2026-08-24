#!/usr/bin/env bash
set -euo pipefail
umask 077

hostname=
secret=
email=
site_dir=
site_upstream=
mtproxy_workers=1
mtproxy_max_connections=4096
public_ip=

usage() {
	echo "usage: sudo ./deploy/install.sh --hostname proxy.example.com --email admin@example.com [--site-dir DIR | --site-upstream URL] [--secret 32-or-34-hex] [--mtproxy-workers 1] [--mtproxy-max-connections 4096] [--public-ip A.B.C.D]" >&2
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--hostname) hostname="${2:-}"; shift 2 ;;
		--secret) secret="${2:-}"; shift 2 ;;
		--email) email="${2:-}"; shift 2 ;;
		--site-dir) site_dir="${2:-}"; shift 2 ;;
		--site-upstream) site_upstream="${2:-}"; shift 2 ;;
		--mtproxy-workers) mtproxy_workers="${2:-}"; shift 2 ;;
		--mtproxy-max-connections) mtproxy_max_connections="${2:-}"; shift 2 ;;
		--public-ip) public_ip="${2:-}"; shift 2 ;;
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
if [[ -n "$public_ip" ]] && [[ ! "$public_ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]; then
	echo "public ip must be a dotted-quad IPv4 address" >&2
	exit 2
fi

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
cat > /etc/tproxy-server/config.json <<EOF
{
  "public_hostname": "$hostname",
  "listen": "127.0.0.1:8080",
  "admin_listen": "127.0.0.1:8081",
$public_source
  "profiles_file": "/run/credentials/tproxy-server.service/profiles.json"
}
EOF
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
# Official MTProxy announces the address of its own outgoing interface in the
# RPC handshake with each Telegram middle-end. On a cloud instance whose public
# address is NAT-ed onto a private interface address, that announcement does not
# match the source address Telegram observes and every middle-end closes the
# connection immediately after the handshake. MTProxy reconnects without any
# backoff, which exhausts the host conntrack table within a minute and breaks
# every stream the relay hands to it. --nat-info states both addresses.
mtproxy_nat_info=
# Ask the kernel which local address reaches a real Telegram middle-end rather
# than a generic public address: on a host where another interface owns the
# default route, only the route MTProxy itself takes identifies the address it
# announces.
nat_probe="$(sed -n 's/^proxy_for[[:space:]]\+-\?[0-9]\+[[:space:]]\+\([0-9.]\+\):[0-9]\+;.*/\1/p' \
	/etc/mtproxy/proxy-multi.conf 2>/dev/null | head -1)"
local_ip="$(ip -4 route get "${nat_probe:-149.154.175.50}" 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
if [[ "$local_ip" =~ ^(10\.|127\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.) ]]; then
	if [[ -z "$public_ip" ]]; then
		# The hostname's A record is by definition this server's public address:
		# Caddy could not have been asked for a certificate for it otherwise.
		public_ip="$(getent ahostsv4 "$hostname" | awk '{print $1; exit}')"
	fi
	if [[ "$public_ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] && [[ "$public_ip" != "$local_ip" ]]; then
		mtproxy_nat_info="--nat-info $local_ip:$public_ip"
		echo "MTProxy NAT mapping: $local_ip -> $public_ip"
	else
		echo "warning: interface address $local_ip is private but no public address was" >&2
		echo "resolved for $hostname; pass --public-ip if MTProxy cannot reach Telegram" >&2
	fi
fi

cat > /etc/mtproxy/mtproxy.env <<EOF
MTPROXY_SECRET=$backend_secret
MTPROXY_WORKERS=$mtproxy_workers
MTPROXY_MAX_CONNECTIONS=$mtproxy_max_connections
MTPROXY_NAT_INFO=$mtproxy_nat_info
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

echo
echo "Installed for https://$hostname/"
echo "Check: systemctl --no-pager --full status caddy mtproxy tproxy-server"
echo "Check: curl --fail https://$hostname/"
echo "Check: curl --fail http://127.0.0.1:8081/readyz"
