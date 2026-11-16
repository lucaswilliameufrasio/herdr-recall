#!/bin/sh
set -eu

usage() {
  printf '%s\n' 'Uso: sh install.sh [--bin-dir DIRETÓRIO] [--allow-downgrade] [--force]'
  printf '%s\n' 'Instala ou atualiza os dois binários deste pacote.'
  printf '%s\n' '  Versão mais nova que a instalada: atualiza.'
  printf '%s\n' '  Mesma versão: não faz nada.'
  printf '%s\n' '  Versão mais antiga: recusa, a não ser com --allow-downgrade.'
  printf '%s\n' '  Versão instalada desconhecida (anterior à v0.4.0 ou build local): recusa, a não ser com --force.'
}

bin_dir="${HOME:?HOME não definido}/.local/bin"
allow_downgrade=0
force=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --bin-dir)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        usage >&2
        exit 2
      fi
      bin_dir=$2
      shift 2
      ;;
    --allow-downgrade) allow_downgrade=1; shift ;;
    --force) force=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

# version_of prints vX.Y.Z from "herdr-recall vX.Y.Z", or nothing.
version_of() {
  line=$("$1" --version 2>/dev/null | head -n 1) || line=''
  case "$line" in
    "herdr-recall v"*) ;;
    *) return 0 ;;
  esac
  candidate=${line#herdr-recall }
  if printf '%s\n' "$candidate" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    printf '%s\n' "$candidate"
  fi
}

# compare_versions prints -1, 0 or 1 for vA against vB.
compare_versions() {
  a=${1#v}
  b=${2#v}
  old_ifs=$IFS
  IFS=.
  # shellcheck disable=SC2086
  set -- $a $b
  IFS=$old_ifs
  for pair in "$1 $4" "$2 $5" "$3 $6"; do
    left=${pair% *}
    right=${pair#* }
    if [ "$left" -lt "$right" ]; then printf '%s\n' -1; return 0; fi
    if [ "$left" -gt "$right" ]; then printf '%s\n' 1; return 0; fi
  done
  printf '%s\n' 0
}

source_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
for name in herdr-recall herdr-recall-server; do
  if [ ! -f "$source_dir/$name" ] || [ ! -x "$source_dir/$name" ]; then
    printf 'Binário ausente no pacote: %s\n' "$name" >&2
    exit 1
  fi
done
new_version=$(version_of "$source_dir/herdr-recall")
if [ -z "$new_version" ]; then
  printf '%s\n' 'O pacote não informa a versão; nada foi instalado.' >&2
  exit 1
fi

existing=0
for name in herdr-recall herdr-recall-server; do
  if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
    existing=1
  fi
  if [ -d "$bin_dir/$name" ]; then
    printf 'O destino é um diretório; nada foi instalado: %s\n' "$bin_dir/$name" >&2
    exit 1
  fi
done

action=instalar
if [ "$existing" -eq 1 ]; then
  old_version=''
  if [ -x "$bin_dir/herdr-recall" ]; then
    old_version=$(version_of "$bin_dir/herdr-recall")
  fi
  if [ -z "$old_version" ]; then
    if [ "$force" -ne 1 ]; then
      printf 'Já existe um herdr-recall em %s com versão desconhecida (anterior à v0.4.0 ou build local).\n' "$bin_dir" >&2
      printf 'Para substituir pela %s, rode de novo com --force. Nada foi instalado.\n' "$new_version" >&2
      exit 1
    fi
    action="substituir a versão desconhecida pela $new_version"
  else
    order=$(compare_versions "$new_version" "$old_version")
    case "$order" in
      0)
        printf 'herdr-recall %s já está instalado em %s. Nada foi alterado.\n' "$new_version" "$bin_dir"
        exit 0
        ;;
      -1)
        if [ "$allow_downgrade" -ne 1 ]; then
          printf 'A versão instalada (%s) é mais nova que a do pacote (%s).\n' "$old_version" "$new_version" >&2
          printf '%s\n' 'Para voltar mesmo assim, rode de novo com --allow-downgrade. Nada foi instalado.' >&2
          exit 1
        fi
        printf 'Aviso: voltando de %s para %s. O estado local gravado pela versão mais nova pode não ser lido pela antiga;\n' "$old_version" "$new_version" >&2
        printf '%s\n' 'a fila e o histórico ficam onde estão.' >&2
        action="voltar de $old_version para $new_version"
        ;;
      1) action="atualizar de $old_version para $new_version" ;;
    esac
  fi
fi

mkdir -p -- "$bin_dir"
staged=''
cleanup() {
  for file in $staged; do
    rm -f -- "$file"
  done
}
trap cleanup EXIT HUP INT TERM
for name in herdr-recall herdr-recall-server; do
  tmp_file="$bin_dir/.$name.new.$$"
  (
    set -C
    umask 022
    cat "$source_dir/$name" > "$tmp_file"
  )
  staged="$staged $tmp_file"
  chmod 755 "$tmp_file"
done
# Each rename is atomic: a running process keeps the old file open.
mv -f -- "$bin_dir/.herdr-recall-server.new.$$" "$bin_dir/herdr-recall-server"
if ! mv -f -- "$bin_dir/.herdr-recall.new.$$" "$bin_dir/herdr-recall"; then
  printf '%s\n' 'Falha ao trocar herdr-recall; o servidor já foi trocado. Rode o instalador de novo.' >&2
  exit 1
fi
staged=''

case "$action" in
  instalar)
    printf 'herdr-recall %s instalado em %s. Nenhum serviço foi instalado ou iniciado.\n' "$new_version" "$bin_dir"
    printf '%s\n' 'Verifique se o diretório está no PATH; configure o agente com herdr-recall setup.'
    ;;
  *)
    printf 'Pronto: %s em %s.\n' "$action" "$bin_dir"
    if [ -f "$HOME/.config/systemd/user/herdr-recall.service" ]; then
      printf '%s\n' 'O serviço segue com o binário antigo até reiniciar: systemctl --user restart herdr-recall'
    elif [ -f "$HOME/Library/LaunchAgents/dev.herdr-recall.agent.plist" ]; then
      printf '%s\n' "O serviço segue com o binário antigo até reiniciar: launchctl kickstart -k gui/$(id -u)/dev.herdr-recall.agent"
    fi
    ;;
esac
