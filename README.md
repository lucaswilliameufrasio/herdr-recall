# herdr-recall

Produto irmão do [ai-usage-tracker](https://github.com/lucaswilliameufrasio/ai-usage-tracker). Guarda a mesa do Herdr — workspace, aba, pane, harness e o comando para retomar a sessão — para consultar depois de uma queda.

O desenho aceito está em [docs/design.md](docs/design.md). O nome é provisório.

O servidor escuta em `127.0.0.1:8788`. O executável do agente é `herdr-recall`. O do servidor é `herdr-recall-server`. Nenhum dos dois se instala com o nome `agent`.

`herdr-recall-server` usa por padrão o banco privado `~/.local/state/herdr-recall-server/server.db` e aceita `--bind` e `--db`. O listener sem TLS é restrito a loopback; um proxy externo pode terminar TLS e encaminhar ao endereço local.

`herdr-recall capture` grava uma captura local e para. Não fala com o servidor.

`herdr-recall service install` exige configuração e token do enrolamento, e liga um serviço de usuário que captura a cada 60 s, grava antes da rede e envia a fila em ordem. `herdr-recall service doctor` verifica o serviço, a configuração, `/healthz` e a autorização do dispositivo; `service install --dry-run` valida os arquivos sem instalar a unidade.

`herdr-recall restore --session NOME` monta a última captura numa sessão nomeada que já está aberta e ainda não tem workspace. `default` fica como está. `--plan` só mostra o que seria feito.
