# Verificação visual do item 10

Verificação em 2026-11-15 com dados sintéticos, SQLite temporário e o handler
real do servidor. O executável `cmd/recall` ainda é um stub: isto **não** é uma
verificação do binário de servidor nem do envio de um agente real.

## Evidências

- [Estados do painel](states.png): lista vazia, máquina sem captura, Herdr sem
  resposta, sem sinal, boot anterior, mesa estável, agente revogado,
  ambiguidade e erro de leitura, nessa ordem.
- [Tabela a 1280 px](desktop-1280.png): a verificação anterior encontrou
  palavras e o botão Restaurar quebrados no meio; a captura registra a correção.
- [Cartões em 360 px](estreito-360.png): dois panes ambíguos, com caminho longo,
  aviso e ação visíveis. A captura é anterior à correção específica da tabela
  larga; o CSS dos cartões não foi alterado depois dela.
- [Sem JavaScript](sem-javascript.png): scripts bloqueados por CSP na resposta
  de teste; `<details>` abre e mostra o comando completo, e os horários
  continuam em UTC. A cópia manual permanece possível.
- [Movimento reduzido](movimento-reduzido.png): Firefox headless em perfil
  temporário, com `ui.prefersReducedMotion=1`. A instrumentação da fixture
  registra `reduce=true`, token `0ms` e transição calculada `0s, 0s`.
- [Pane sem sessão](sem-sessao.png) e [em 360 px](sem-sessao-360.png):
  apresenta a mensagem e não inclui botão nem `details`.
- [Captura existente com zero panes](captura-zero.png): a linha de captura
  existe no banco, mas a tabela é substituída pela mensagem vazia.
- [Herdr sem resposta com captura](herdr-sem-resposta-com-captura.png) e
  [boot anterior selecionado quando há captura mais nova](boot-anterior-selecionado.png):
  preservam a mesa e mostram a frase correspondente.
- [Cópia pendente](copia-pendente.png) e [falha de cópia](copia-falhou.png):
  clipboard simulado pela fixture. O texto `fixture: aria-busy=...` é uma sonda
  da fixture, não conteúdo da aplicação; ela confirmou `true` enquanto a
  promise estava pendente e ausência do atributo após a rejeição.
- [Comando aberto](comando-aberto.png) e [foco em Restaurar](foco-restaurar.png):
  a linha selecionada recebe a cor de seleção e o anel de teclado é visível.
- [Restaurar sob hover](restaurar-hover.png) e
  [pressionado](restaurar-pressionado.png), e
  [botão secundário pressionado](secundario-pressionado.png): Firefox headless
  isolado com WebDriver BiDi; o ponteiro foi mantido pressionado durante a
  captura. O navegador confirmou `:active` e as cores computadas esperadas.
- [Restaurar desabilitado](restaurar-desabilitado-fixture.png),
  [secundário desabilitado](secundario-desabilitado-fixture.png) e
  [campo desabilitado](campo-desabilitado-fixture.png): o atributo `disabled`
  foi aplicado artificialmente na fixture para inspecionar as regras visuais;
  esses estados não representam um fluxo real observado. Sem sessão, a tela
  real não renderiza o botão de Restaurar.
- [Foco no campo](campo-foco.png), [senha incorreta](login-erro.png),
  [confirmação divergente](setup-confirmacao-erro.png) e
  [setup concluído](setup-concluido.png): no Firefox, uma confirmação diferente
  impede a criação e marca o campo; a confirmação correta cria o operador e
  oferece o login, que foi percorrido até a lista. O endpoint JSON e clientes
  antigos de formulário sem o novo campo continuam aceitos.

No Firefox também foram verificados o foco por Tab, a navegação da lista ao
detalhe, a mensagem da senha incorreta, o formulário de gerar código com CSRF,
o retorno à lista e a saída para a tela de login. O código da captura foi
sintético e não foi salvo nesta documentação.

## Reprodução local

1. Execute `RECALL_VISUAL_FIXTURE=1 go test ./internal/server -run '^TestVisualFixture$' -count=1 -v`.
2. Enquanto o teste estiver ativo, abra `http://127.0.0.1:18788/__empty` e
   `http://127.0.0.1:18788/maquinas/{empty,zero,no-session,down,down-desk,offline,previous,two-boots,stable,revoked,ambiguous}`.
   Para uma área de 1280 px, abra `/__viewport/ambiguous`.
3. Para bloquear scripts, acrescente `?nojs=1` à URL de uma máquina. Para
   mostrar o erro de leitura sintético, abra `/__inject-error` depois das
   outras telas; essa ação corrompe **somente** o banco temporário da fixture.
4. Para encerrar, abra `/__done`. O teste fecha o listener e descarta seu
   banco temporário. A fixture autentica automaticamente com uma sessão
   sintética e só escuta em loopback; nunca a use como servidor de produção.

Para simular falha ou pendência do clipboard, abra respectivamente
`/maquinas/revoked?clipboard=fail` ou `?clipboard=pending` e clique em
Restaurar. A fixture substitui somente o clipboard daquela página; o sucesso
também foi conferido separadamente com o clipboard real do navegador.

O teste de movimento reduzido usa `/__motion`, que apenas instrumenta a página
da fixture. O Firefox foi iniciado com `--headless --no-remote`, um perfil
descartável sob `/tmp/opencode` e a preferência acima; o perfil habitual não foi
alterado.

Limites: os estados desabilitados foram induzidos na fixture, não observados em
uma jornada real. A fixture não representa macOS, dispositivo físico ou
aplicação implantada.
