#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  printf '%s\n' 'Uso: sh scripts/package-release.sh vX.Y.Z DIRETÓRIO-NOVO [--allow-dirty]' >&2
  exit 2
fi
tag=$1
output=$2
allow_dirty=${3:-}
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf '%s\n' 'Versão inválida.' >&2
  exit 2
fi
if [ -n "$allow_dirty" ] && [ "$allow_dirty" != '--allow-dirty' ]; then
  printf '%s\n' 'Opção inválida.' >&2
  exit 2
fi
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
dirty=$(git status --porcelain)
if [ -n "$dirty" ] && [ "$allow_dirty" != '--allow-dirty' ]; then
  printf '%s\n' 'Árvore alterada. Para testes locais apenas, use --allow-dirty.' >&2
  exit 1
fi
if [ -e "$output" ] || [ -L "$output" ]; then
  printf '%s\n' 'Diretório de saída já existe; escolha outro.' >&2
  exit 1
fi
mkdir -p "$(dirname -- "$output")"
mkdir "$output"
output=$(CDPATH='' cd -- "$output" && pwd)
if [ -n "$dirty" ]; then
  printf '%s\n' 'NÃO PUBLICAR: construído com alterações locais.' > "$output/LOCAL-ONLY"
fi
git rev-parse HEAD > "$output/COMMIT"
revision=$(git rev-parse HEAD)
built_at=$(git log -1 --format=%cI HEAD)
agent_ldflags="-s -w -X main.version=$tag -X main.commit=$revision -X main.buildDate=$built_at"
server_ldflags="-s -w -X main.version=$tag"
sed "s/@RELEASE_TAG@/$tag/g" scripts/herdr-recall-installer.sh > "$output/herdr-recall-installer.sh"
for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  os=${target%-*}
  arch=${target#*-}
  mkdir "$output/$target"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$agent_ldflags" -o "$output/$target/herdr-recall" ./cmd/agent
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$server_ldflags" -o "$output/$target/herdr-recall-server" ./cmd/recall
  cp install.sh README.md "$output/$target/"
  tar -C "$output/$target" -czf "$output/herdr-recall-$tag-$target.tar.gz" herdr-recall herdr-recall-server install.sh README.md
done
(
  cd "$output"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "herdr-recall-$tag-"*.tar.gz > SHA256SUMS
  else
    shasum -a 256 "herdr-recall-$tag-"*.tar.gz > SHA256SUMS
  fi
)
cat > "$output/RELEASE-NOTES.md" <<EOF
## Instalação ($tag)

Linux e macOS (amd64 e arm64):

\`\`\`sh
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/herdr-recall-installer.sh | sh
\`\`\`

O instalador verifica SHA-256 e instala agente e servidor em \`~/.local/bin\`. Sobre uma versão mais antiga, atualiza; sobre a mesma, não faz nada; um downgrade exige \`--allow-downgrade\` e uma instalação sem versão (v0.3.0 ou anterior) exige \`--force\`, passados com \`| sh -s -- --force\`. Depois, \`herdr-recall update\` atualiza sozinho. Consulte o [guia de primeira execução](https://github.com/lucaswilliameufrasio/herdr-recall/blob/$tag/README.md#primeira-execução-na-mesma-máquina).
O checksum detecta corrupção, mas não protege contra substituição da release e do checksum juntos.

## Downloads

| Plataforma | Pacote | Checksums |
|---|---|---|
| Linux x64 | [linux-amd64](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/herdr-recall-$tag-linux-amd64.tar.gz) | [SHA256SUMS](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/SHA256SUMS) |
| Linux ARM64 | [linux-arm64](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/herdr-recall-$tag-linux-arm64.tar.gz) | [SHA256SUMS](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/SHA256SUMS) |
| macOS Intel | [darwin-amd64](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/herdr-recall-$tag-darwin-amd64.tar.gz) | [SHA256SUMS](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/SHA256SUMS) |
| macOS Apple Silicon | [darwin-arm64](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/herdr-recall-$tag-darwin-arm64.tar.gz) | [SHA256SUMS](https://github.com/lucaswilliameufrasio/herdr-recall/releases/download/$tag/SHA256SUMS) |

Windows não é suportado. Os binários macOS não são notarizados.
EOF
printf 'Pacotes preparados em %s. Nenhuma release foi publicada.\n' "$output"
