# herdr-recall

Produto irmão do [ai-usage-tracker](https://github.com/lucaswilliameufrasio/ai-usage-tracker). Guarda a mesa do Herdr — workspace, aba, pane, harness e o comando para retomar a sessão — para consultar depois de uma queda.

O desenho aceito está em [docs/design.md](docs/design.md). O nome é provisório.

O servidor escuta em `127.0.0.1:8788`. O executável do agente é `herdr-recall`. O do servidor é `herdr-recall-server`. Nenhum dos dois se instala com o nome `agent`.

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

`herdr-recall restore --session NOME` monta a última captura numa sessão nomeada que já está aberta e ainda não tem workspace. `default` fica como está. `--plan` só mostra o que seria feito.
