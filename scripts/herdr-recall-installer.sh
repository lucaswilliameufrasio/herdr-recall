#!/bin/sh
set -eu

# Replaced by package-release.sh for the versioned release asset.
tag='@RELEASE_TAG@'
bin_dir="${HOME:?HOME não definido}/.local/bin"

usage() {
  printf '%s\n' 'Uso: sh herdr-recall-installer.sh [--tag vX.Y.Z] [--bin-dir DIRETÓRIO]'
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag|--bin-dir)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        usage >&2
        exit 2
      fi
      if [ "$1" = "--tag" ]; then tag=$2; else bin_dir=$2; fi
      shift 2
      ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf '%s\n' 'Informe uma versão com --tag vX.Y.Z.' >&2
  exit 2
fi
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) printf '%s\n' 'Sistema não suportado: use Linux ou macOS.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf '%s\n' 'Arquitetura não suportada.' >&2; exit 1 ;;
esac
for name in herdr-recall herdr-recall-server; do
  if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
    printf 'Destino já existe; nada foi instalado: %s\n' "$name" >&2
    exit 1
  fi
done
if ! command -v curl >/dev/null 2>&1; then
  printf '%s\n' 'curl é necessário para baixar a release.' >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  hash_tool=shasum
else
  printf '%s\n' 'É necessário sha256sum ou shasum.' >&2
  exit 1
fi
tmp=$(mktemp -d "${TMPDIR:-/tmp}/herdr-recall-install.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT HUP INT TERM
archive="herdr-recall-$tag-$os-$arch.tar.gz"
base="https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --connect-timeout 15 --max-time 300 "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --connect-timeout 15 --max-time 300 "$base/$archive" -o "$tmp/$archive"
expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp/SHA256SUMS")
case "$expected" in
  *[!a-fA-F0-9]*|'') printf '%s\n' 'Checksum ausente ou inválido.' >&2; exit 1 ;;
esac
if [ "${#expected}" -ne 64 ]; then
  printf '%s\n' 'Checksum inválido.' >&2
  exit 1
fi
if [ "$hash_tool" = sha256sum ]; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
fi
if [ "$actual" != "$expected" ]; then
  printf '%s\n' 'Checksum não confere; nada foi instalado.' >&2
  exit 1
fi
for name in herdr-recall herdr-recall-server install.sh; do
  tar -xOzf "$tmp/$archive" "$name" > "$tmp/$name"
  if [ ! -s "$tmp/$name" ]; then
    printf 'Arquivo inválido no pacote: %s\n' "$name" >&2
    exit 1
  fi
done
chmod 700 "$tmp/herdr-recall" "$tmp/herdr-recall-server"
printf 'Pacote %s verificado por SHA-256.\n' "$archive"
sh "$tmp/install.sh" --bin-dir "$bin_dir"
