#!/bin/sh
set -eu

usage() {
  printf '%s\n' 'Uso: sh install.sh [--bin-dir DIRETÓRIO]'
  printf '%s\n' 'Instala os dois binários deste pacote sem substituir arquivos existentes.'
}

bin_dir="${HOME:?HOME não definido}/.local/bin"
if [ "$#" -gt 0 ]; then
  case "$1" in
    --bin-dir)
      if [ "$#" -ne 2 ] || [ -z "$2" ]; then
        usage >&2
        exit 2
      fi
      bin_dir=$2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
fi

source_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
for name in herdr-recall herdr-recall-server; do
  if [ ! -f "$source_dir/$name" ] || [ ! -x "$source_dir/$name" ]; then
    printf 'Binário ausente no pacote: %s\n' "$name" >&2
    exit 1
  fi
  if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
    printf 'Destino já existe; nada foi instalado: %s\n' "$name" >&2
    exit 1
  fi
done

mkdir -p -- "$bin_dir"
(
  set -C
  umask 022
  cat "$source_dir/herdr-recall" > "$bin_dir/herdr-recall"
)
if ! (
  set -C
  umask 022
  cat "$source_dir/herdr-recall-server" > "$bin_dir/herdr-recall-server"
); then
  printf '%s\n' 'Falha no segundo binário; verifique a instalação parcial.' >&2
  exit 1
fi
chmod 755 "$bin_dir/herdr-recall" "$bin_dir/herdr-recall-server"
printf '%s\n' 'Binários instalados. Nenhum serviço foi instalado ou iniciado.'
printf '%s\n' 'Verifique se o diretório está no PATH; configure o agente com herdr-recall setup.'
