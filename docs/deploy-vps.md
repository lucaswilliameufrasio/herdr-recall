# VPS Linux com Docker Compose

Este procedimento prepara **uma instância** do servidor. O agente continua em
cada máquina com Herdr; não é executado no container. A VPS executa uma imagem
construída e testada fora dela. Não há blue-green: a troca do container causa
indisponibilidade breve, e rollback de imagem não desfaz migração do SQLite.

**Ainda não validado em uma VPS:** valide a imagem, o backup/restauração e o
proxy em ambiente isolado antes de usá-los em produção. Não exponha o painel
enquanto essas verificações não estiverem concluídas.

## Condições de segurança

- Linux com Docker Engine e Compose v2. `network_mode: host` é necessário
  porque o servidor aceita somente loopback. Ele compartilha a rede do host:
  qualquer processo local pode alcançar `127.0.0.1:8788`. Restrinja o acesso
  à VPS e não execute workloads não confiáveis nela. Não publique essa porta
  no firewall nem use `ports:` para tentar isolá-la. Em Docker rootless,
  dependendo da versão e configuração, a rede `host` pode não ser a rede
  real da VPS; confirme explicitamente que o proxy alcança o container. Se
  não alcançar, esta configuração não serve para esse daemon.
- O servidor considera o *bind* local suficiente para permitir o setup inicial,
  inclusive quando recebe tráfego de um proxy. **Bloqueie `/acesso/setup` no
  proxy público antes de iniciar** e faça o setup via túnel SSH privado. O proxy
  deve negar também variantes normalizadas da rota.
- O servidor não confia em cabeçalhos de proxy e, nessa topologia, não marca
  o cookie como `Secure`. Configure o proxy TLS para acrescentar `Secure` ao
  cookie `recall_session` e confirme o nome real no `Set-Cookie` recebido;
  não exponha HTTP público. Verifique também `HttpOnly` e `SameSite`.
- O banco contém caminhos e identificadores privados. Proteja host, diretório,
  backups, acesso SSH, proxy e certificado. Um healthcheck não verifica backup.

## 1. Preparar imagem fora da VPS

Use um pacote de release Linux da arquitetura da VPS e confira `SHA256SUMS`
conforme o [README](../README.md#instalação). Em uma máquina de build com Docker,
extraia o pacote num diretório novo e confirme o caminho do executável
`herdr-recall-server`. Copie o executável e `deploy/Dockerfile` para um
diretório de build vazio. A versão do binário deve ser a desejada:

```sh
./herdr-recall-server --version
docker build -t REGISTRY/PROJETO:VERSAO .
```

Substitua o nome da imagem e publique-a no seu registry com o procedimento
dele. Registre o **digest** retornado pelo registry e confira a arquitetura da
imagem; ao montar em máquina de arquitetura diferente, ajuste a plataforma do
build para corresponder ao binário. Não use `latest` na VPS. A imagem não
contém shell nem utilitários.

## 2. Preparar a VPS

Escolha um diretório absoluto de dados persistente, fora de releases e do
checkout. No host, crie-o vazio com acesso somente ao UID/GID `65532:65532`
do container; em Docker rootless, mapeamento de UID pode exigir configuração
específica. Confira escrita pelo container antes de servir tráfego. Não altere
permissões de um diretório que já contenha dados sem verificar seu proprietário.
Não use armazenamento efêmero.

Transfira apenas `deploy/compose.yaml` e configure o ambiente do operador.
Os exemplos de variáveis abaixo são para um shell POSIX (`sh`), não Fish:

```sh
export RECALL_IMAGE='REGISTRY/PROJETO@sha256:DIGEST_COMPLETO'
export RECALL_DATA_DIR='/CAMINHO/ABSOLUTO/DADOS'
docker compose -f compose.yaml config
```

Substitua os valores pelos reais. Confirme a imagem por digest e o caminho
antes de continuar. Instale e configure separadamente um proxy HTTPS existente,
com a proteção descrita acima; o upstream é `http://127.0.0.1:8788`. Não
sobrescreva a configuração compartilhada de outros serviços. Valide a
configuração do proxy antes de recarregá-la.

## 3. Subir e inicializar

Confirme que a porta 8788 está livre e que o bloqueio do setup no proxy já foi
aplicado. Então:

```sh
docker compose -f compose.yaml pull
docker compose -f compose.yaml up -d
curl -fsS http://127.0.0.1:8788/healthz
```

A última linha deve responder `ok`. Se falhar, pare e examine
`docker compose -f compose.yaml logs --tail=100`; não prossiga com o setup.
Nunca inclua tokens, senhas ou dados de sessão em logs compartilhados.

No computador do operador, abra um túnel SSH para a VPS:

```sh
ssh -N -L 8788:127.0.0.1:8788 USUARIO@VPS
```

Com o túnel aberto, acesse `http://127.0.0.1:8788/acesso/setup` no navegador
local, crie a senha e entre. Feche o túnel ao terminar. Confirme que
`https://SEU-DOMINIO/acesso/setup` é negado pelo proxy e que a resposta de login
HTTPS inclui `Secure`, `HttpOnly` e `SameSite` no cookie. Teste o painel HTTPS e
o enrolamento de um agente pela URL HTTPS; só então considere liberar o uso.

## Atualização e rollback

Antes de atualizar, faça e teste um backup consistente como abaixo. Registre o
digest atual; obtenha e valide o novo digest fora da VPS. Defina `RECALL_IMAGE`
com o novo digest, execute novamente os comandos do passo 3 e verifique o
healthcheck **e** o acesso HTTPS. Uma falha pode deixar o serviço indisponível;
não existe troca automática nem bloqueio entre deploys simultâneos. Não rode
dois deploys ao mesmo tempo.

Para tentar rollback, pare a alteração de tráfego e consulte a compatibilidade
da versão anterior com o esquema atual. Se compatível, defina o digest anterior
e execute `docker compose -f compose.yaml up -d`; confira saúde e HTTPS. Se
incompatível, restaurar o backup anterior também reverte dados recebidos desde
o backup: avalie essa perda antes de qualquer restauração. Não execute duas
versões simultaneamente sobre o mesmo banco.

## Backup e restauração

O SQLite pode usar arquivos auxiliares; copiar apenas `server.db` com o servidor
ativo não é um backup consistente. Na ausência de ferramenta SQLite de backup
validada nesta instalação, planeje uma janela, pare somente este serviço com
`docker compose -f compose.yaml stop recall`, confirme que parou, e copie o
diretório de dados inteiro para armazenamento privado externo. Reinicie com
`docker compose -f compose.yaml start recall` e verifique `/healthz`. Preserve
os arquivos originais e teste a restauração em outro diretório e outra instância
isolada, sem apontar agentes para ela. Não substitua o banco de produção sem
avaliar perda de dados, parar o serviço e preservar uma cópia do estado atual.

## Uirapuru, Railway e k3s

O contrato portátil aqui é imagem imutável, diretório persistente, instância
única e verificação HTTP. Uma integração com Uirapuru ainda precisa ser
adaptada e testada conforme a interface e permissões reais dele; não há
integração pronta neste repositório. Railway e k3s não estão configurados nem
validados: requerem solução própria para loopback/proxy, volume persistente,
setup seguro e backup. Não use este Compose como manifesto dessas plataformas.
