#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
	echo "run this installer as root" >&2
	exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
	echo "the stock official MTProxy build requires an x86_64 server" >&2
	exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl build-essential iproute2 libssl-dev util-linux zlib1g-dev

if ! id mtproxy >/dev/null 2>&1; then
	useradd --system --home /nonexistent --shell /usr/sbin/nologin mtproxy
fi

source_directory=/opt/MTProxy
mtproxy_commit=f36d8af769ffaeac36978d38c2c0f6d1104c2137
mtproxy_checksum=919795c416b870670841a21d1930ad97a24c7b84b9eb8c6f9e3de32f2fdf4655
if [[ ! -x "$source_directory/objs/bin/mtproto-proxy" ]] ||
	[[ ! -f "$source_directory/.tproxy-commit" ]] ||
	! grep -Fxq "$mtproxy_commit" "$source_directory/.tproxy-commit"; then
	temporary="$(mktemp -d /tmp/mtproxy-build.XXXXXX)"
	chmod 0711 "$temporary"
	trap 'rm -rf "$temporary"' EXIT
	archive="$temporary/MTProxy.tar.gz"
	build_directory="$temporary/MTProxy"
	curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
		--output "$archive" \
		"https://github.com/TelegramMessenger/MTProxy/archive/${mtproxy_commit}.tar.gz"
	test "$(sha256sum "$archive" | awk '{print $1}')" = "$mtproxy_checksum"
	install -d -o mtproxy -g mtproxy -m 0755 "$build_directory"
	tar -C "$build_directory" --strip-components=1 -xzf "$archive"
	chown -R mtproxy:mtproxy "$build_directory"
	runuser -u mtproxy -- make -C "$build_directory" -j"$(nproc)"
	test -x "$build_directory/objs/bin/mtproto-proxy"
	printf '%s\n' "$mtproxy_commit" > "$build_directory/.tproxy-commit"
	chown -R root:root "$build_directory"
	if [[ -e "$source_directory" ]]; then
		mv "$source_directory" "$source_directory.before-tproxy.$(date +%Y%m%d%H%M%S)"
	fi
	mv "$build_directory" "$source_directory"
	trap - EXIT
	rm -rf "$temporary"
fi

# install.sh runs with umask 077, so make's output keeps owner-only
# permissions; the unprivileged mtproxy service user must be able to
# traverse the tree and execute the binary. Normalize permissions
# unconditionally: when the guard above skipped a rebuild (a previous
# failed run may already have left /opt/MTProxy with a root-owned
# 0700 binary), the tree is reused as-is and still needs this fix.
chown -R root:root "$source_directory"
chmod -R a+rX "$source_directory"

install -d -o root -g mtproxy -m 0750 /etc/mtproxy
secret_temp="$(mktemp /etc/mtproxy/proxy-secret.XXXXXX)"
config_temp="$(mktemp /etc/mtproxy/proxy-multi.conf.XXXXXX)"
trap 'rm -f "$secret_temp" "$config_temp"' EXIT
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
	--output "$secret_temp" https://core.telegram.org/getProxySecret
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
	--output "$config_temp" https://core.telegram.org/getProxyConfig
test "$(wc -c < "$secret_temp")" -eq 128
test "$(wc -c < "$config_temp")" -ge 100
grep -q '^default ' "$config_temp"
grep -q '^proxy_for ' "$config_temp"
chown root:mtproxy "$secret_temp" "$config_temp"
chmod 0640 "$secret_temp" "$config_temp"
mv -f "$secret_temp" /etc/mtproxy/proxy-secret
mv -f "$config_temp" /etc/mtproxy/proxy-multi.conf
trap - EXIT
