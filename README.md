# herdr-recall

Produto irmão do [ai-usage-tracker](https://github.com/lucaswilliameufrasio/ai-usage-tracker). Guarda a mesa do Herdr — workspace, aba, pane, harness e o comando para retomar a sessão — para consultar depois de uma queda.

O desenho aceito está em [docs/design.md](docs/design.md). O nome é provisório.

O servidor escuta em `127.0.0.1:8788`. O executável do agente é `herdr-recall`. O do servidor é `herdr-recall-server`. Nenhum dos dois se instala com o nome `agent`.

Para preparar uma VPS Linux com Docker Compose, consulte [deploy em VPS](docs/deploy-vps.md). A configuração ainda precisa ser validada em ambiente isolado antes de produção.

### Instalação

O instalador externo seleciona Linux ou macOS e amd64 ou arm64, baixa o pacote correspondente e verifica seu SHA-256. Ele está disponível a partir da v0.3.0; a v0.2.0 não o possui:

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/lucaswilliameufrasio/herdr-recall/releases/latest/download/herdr-recall-installer.sh | sh
```

O checksum detecta corrupção no download, mas não protege se o pacote e o checksum da release forem ambos substituídos. O instalador copia os dois binários para `~/.local/bin`; não usa sudo, não altera o PATH nem inicia serviços.

A partir da v0.4.0 o instalador compara a versão do pacote com a instalada:

| Situação | O que acontece |
|---|---|
| Nada instalado | instala |
| Pacote mais novo | atualiza os dois binários, cada um com uma renomeação atômica |
| Mesma versão | não altera nada e sai com sucesso |
| Pacote mais antigo | recusa; `--allow-downgrade` aceita e avisa |
| Instalada sem versão (v0.3.0 ou anterior, ou build local) | recusa; `--force` substitui |

Com `curl | sh`, passe as opções depois de `sh -s --`. Por exemplo, para sair da v0.3.0:

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/lucaswilliameufrasio/herdr-recall/releases/latest/download/herdr-recall-installer.sh | sh -s -- --force
```

Depois de instalada, a v0.4.0 se atualiza sozinha com `herdr-recall update` (`--check` só consulta; `--restart` reinicia o serviço). O serviço continua com o binário antigo até reiniciar, e os dois comandos avisam isso.

**Enquanto isso, a v0.2.0 pode ser instalada manualmente:**

1. Abra [a release v0.2.0](https://github.com/lucaswilliameufrasio/herdr-recall/releases/tag/v0.2.0), baixe `SHA256SUMS` e o arquivo adequado: `linux-amd64` (Linux x64), `linux-arm64` (Linux ARM64), `darwin-amd64` (Mac Intel) ou `darwin-arm64` (Apple Silicon).
2. No terminal, entre no diretório dos downloads. Substitua `ARQUIVO` abaixo pelo nome exato do pacote baixado e confira se os dois hashes são iguais:

   ```sh
   grep 'ARQUIVO' SHA256SUMS
   shasum -a 256 ARQUIVO
   ```

   No Linux, se `shasum` não estiver disponível, use `sha256sum ARQUIVO`. Se os hashes diferirem, pare: não extraia nem instale.
3. Extraia para uma pasta nova e instale, substituindo `ARQUIVO`:

   ```sh
   mkdir herdr-recall-pacote
   tar -xzf ARQUIVO -C herdr-recall-pacote
   sh herdr-recall-pacote/install.sh
   ```

   A mensagem `herdr-recall vX.Y.Z instalado em …` confirma a cópia. Se já houver uma instalação, valem as regras da tabela acima (`--allow-downgrade`, `--force`). Para instalar outra versão ao lado, escolha um diretório novo com `sh herdr-recall-pacote/install.sh --bin-dir "$HOME/herdr-recall-novo/bin"` e use esse caminho explicitamente.

### Comandos

`herdr-recall --help` lista tudo; cada comando tem `--help` com exemplos. Num terminal, `herdr-recall` sem argumentos abre a TUI: capturas à esquerda, panes à direita, `/` filtra, `c` copia o comando (OSC52, nada é executado) e `r` monta a captura numa sessão nomeada vazia, mostrando o plano antes.

| Grupo | Comandos |
|---|---|
| Começo | `setup`, `enroll`, `status` (resumo com os checks do doctor; `--json`) |
| Mesa | `latest` (`--json`, `--workspace`, `--harness`), `history` e `history show N`, `restore --session NOME [--plan] [--from N]`, `capture` |
| Agente e diagnóstico | `service install/uninstall/doctor`, `run`, `verify [--send]`, `queue` (só metadados da fila e da quarentena), `logs [-f]`, `config [show/path]` |
| Manutenção | `update`, `uninstall [--dry-run] [--purge]`, `version`, `completion bash/zsh/fish` |

Uso inválido sai com código 2. `status` e `queue` saem com 1 quando há falha ou quarentena. Fora de um terminal, as cores somem; `NO_COLOR` também as desliga.

Para o autocompletar no Fish: `herdr-recall completion fish > ~/.config/fish/completions/herdr-recall.fish`.

### Primeira execução na mesma máquina

Esta sequência usa o servidor local e requer o Herdr instalado e em execução. O servidor permanece em primeiro plano; a instalação não cria serviço para ele.

1. Confira que os binários estão no PATH. No Bash/Zsh use `export PATH="$HOME/.local/bin:$PATH"`; no Fish use `fish_add_path ~/.local/bin` (persiste no Fish). Para outro diretório de instalação, use o caminho completo dos binários.
2. Em um terminal, inicie `herdr-recall-server`. Ele cria o banco privado em `~/.local/state/herdr-recall-server/server.db`; deixe esse terminal aberto. Em outro terminal, `curl -fsS http://127.0.0.1:8788/healthz` deve responder `ok`. Se falhar, não continue.
3. Abra `http://127.0.0.1:8788/acesso/setup` no navegador da mesma máquina. Crie a senha do operador com pelo menos 12 caracteres e no máximo 72 bytes; depois abra o link **Entrar** e autentique-se. O setup inicial só funciona antes da criação do operador.
4. No painel, clique **Gerar código**. Guarde o código sem compartilhá-lo; ele vale por 10 minutos e só pode ser usado uma vez.
5. Em outro terminal, rode `herdr-recall setup`. Em **URL do servidor**, digite `http://127.0.0.1:8788`; em **Código**, digite o código do painel; em **Nome da máquina**, escolha um nome. O comando grava configuração e token privados; não instale o serviço se o enrolamento falhar.
6. Rode `herdr-recall service install --dry-run` para inspecionar a unidade sem gravar. Depois, se quiser que o agente rode em segundo plano, execute `herdr-recall service install`. Isso instala o serviço **do agente**, não o do servidor.
7. Rode `herdr-recall service doctor`. Verifique os resultados `ok`; corrija qualquer `fail` antes de continuar. Para provocar uma captura e envio reais, rode explicitamente `herdr-recall verify --send`: isso grava e envia a mesa e a fila anterior. Volte ao painel e abra a máquina para conferir a captura. Esse comando não testa restauração.

Para um agente em outra máquina, `127.0.0.1` aponta para a própria máquina do agente, não para o servidor. O servidor aceita apenas loopback; use um proxy TLS ou túnel seguro configurado para alcançar o servidor e informe no setup a URL acessível ao agente. Configurar e proteger esse acesso é uma etapa separada; não exponha o listener HTTP diretamente.

Não há instalador para Windows. Os binários de macOS não foram notarizados; se o macOS bloquear a execução, examine o aviso e a procedência do arquivo antes de decidir como proceder, sem desativar proteções globalmente.

### Preparar uma release

Com o checkout limpo e o commit desejado já revisado, `sh scripts/package-release.sh vX.Y.Z /caminho/novo/para/pacotes` grava a versão, o commit e a data do commit nos binários (`herdr-recall --version` imprime `herdr-recall vX.Y.Z`, que o instalador lê) e gera os quatro arquivos, `SHA256SUMS`, o instalador externo, `COMMIT` e um modelo `RELEASE-NOTES.md` com instalação e tabela de downloads. O diretório de saída não pode existir. Confira os pacotes e checksums, acrescente ao modelo as mudanças e os testes efetivamente realizados e publique somente com autorização. `--allow-dirty` existe apenas para ensaios: gera `LOCAL-ONLY`, e os arquivos resultantes não devem ser publicados. O script não cria tag nem publica release.

`herdr-recall-server` usa por padrão o banco privado `~/.local/state/herdr-recall-server/server.db` e aceita `--bind` e `--db`. O listener sem TLS é restrito a loopback; um proxy externo pode terminar TLS e encaminhar ao endereço local.

`herdr-recall capture` grava uma captura local e para. Não fala com o servidor.

`herdr-recall service install` exige configuração e token do enrolamento, e liga um serviço de usuário que captura a cada 60 s, grava antes da rede e envia a fila em ordem. `herdr-recall service doctor` verifica o serviço, a configuração, `/healthz` e a autorização do dispositivo; `service install --dry-run` valida os arquivos sem instalar a unidade.

`herdr-recall verify` executa o diagnóstico sem capturar nem enviar dados. Para verificar o envio real, execute explicitamente `herdr-recall verify --send`: ele captura a mesa atual, grava presença e uma nova captura no estado local e envia a fila anterior em ordem até o servidor confirmar essa captura. O servidor poderá armazenar os dados. A confirmação HTTP não garante que a captura esteja visível no painel nem testa restauração; uma captura antiga pode não substituir uma mais recente no servidor. Falhas de envio e conflitos resultam em saída não zero: itens não confirmados ficam na fila, e conflitos são preservados na quarentena. Se houver conflito anterior ou surgir um conflito durante a verificação, ela para sem informar sucesso. Após falha de sincronização local, o estado é informado como incerto; use `service doctor` para investigar. O comando não instala nem reinicia serviços.

### Investigar envio e quarentena

Na máquina onde o agente roda, execute `herdr-recall service doctor`. Os números são uma leitura pontual e podem mudar durante o envio; um item pendente pode ser apenas uma tentativa ainda não concluída.

1. Se a fila continuar crescendo, confira no diagnóstico o serviço, a conectividade e a autorização. O agente tenta novamente na próxima volta. No Linux, consulte `journalctl --user -u herdr-recall -n 30`; no macOS, `tail -n 30 ~/Library/Logs/herdr-recall.log`.
2. Se o servidor recusou o token, siga a orientação do `doctor` para enrolar novamente; não inclua token ou código em mensagens de suporte.
3. Se houver itens em quarentena, o diagnóstico retorna falha (saída não zero). Preserve os arquivos locais. Um conflito de captura pode indicar que o mesmo identificador já foi recebido com conteúdo diferente. Confirme a causa antes de decidir uma recuperação; não apague nem reenvie automaticamente. Não compartilhe o conteúdo do spool: ele pode conter caminhos e dados de sessão.
4. Se o diagnóstico não conseguir ler a fila ou a quarentena, preserve os arquivos e investigue permissões e integridade. Uma falha ao sincronizar pode deixar o estado incerto; não presuma que o servidor não recebeu o item.

O agente tenta informar ao servidor apenas as contagens locais antes e depois de cada volta de envio. O painel mostra a última leitura como recente, antiga ou indisponível; ela pode ter mudado depois da observação e não prova que a máquina esteja online. A fila de capturas continua independente desse relato, e falhas de comunicação podem deixar os dados antigos.

Para validar o adaptador contra uma sessão Herdr real, `HERDR_RECALL_LIVE_TEST=1 go test -count=1 ./internal/server -run '^TestLiveHerdrDelivery$' -v` usa um servidor loopback e banco e estado temporários. A captura contém dados privados da sessão, mas o teste informa somente contagens e remove seus arquivos temporários ao terminar. A suíte normal não acessa o Herdr real.

`herdr-recall restore --session NOME` monta a última captura numa sessão nomeada que já está aberta e ainda não tem workspace. `default` fica como está. `--plan` só mostra o que seria feito.
