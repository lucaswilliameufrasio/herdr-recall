# herdr-recall

Produto irmão do [ai-usage-tracker](https://github.com/lucaswilliameufrasio/ai-usage-tracker). Guarda a mesa do Herdr — workspace, aba, pane, harness e o comando para retomar a sessão — para consultar depois de uma queda.

O desenho aceito está em [docs/design.md](docs/design.md). O nome é provisório.

O servidor escuta em `127.0.0.1:8788`. O executável do agente é `herdr-recall`. O do servidor é `herdr-recall-server`. Nenhum dos dois se instala com o nome `agent`.

`herdr-recall capture` grava uma captura local e para. Não fala com o servidor.

`herdr-recall restore --session NOME` monta a última captura numa sessão nomeada que já está aberta e ainda não tem workspace. `default` fica como está. `--plan` só mostra o que seria feito.
