# Revisão de qualidade do Virgil

## Atualização de correção — 2026-09-17

Os três achados de código desta revisão foram corrigidos em commits locais do repositório Virgil: `16201b5` (gofmt), `6580bd1` (prazo de leitura HTTP) e `7991577` (shutdown com prazo). Não houve push. Os dois arquivos de formatação foram alterados apenas por `gofmt`.

O servidor agora define `ReadTimeout: 15 * time.Second` para o request completo, além de `ReadHeaderTimeout: 5 * time.Second`. O corpo do chat mantém o limite de 1 MiB. Timeout de leitura retorna HTTP 408 com código `request_timeout`, excesso de tamanho retorna 413 com `request_too_large`, e outros erros de leitura retornam 400 sem incluir o erro interno na resposta. Não foi definido `WriteTimeout` global para não interromper SSE. O shutdown cancela os contextos de requests e streams ativos, aguarda até cinco segundos e fecha conexões restantes se o prazo expirar; falhas de listen e de shutdown são retornadas.

Os testes de regressão exercitam o servidor HTTP real com `httptest` ou listener local: corpo lento, corpo acima do limite, JSON normal, primeiro frame SSE antes do fim do provedor, cancelamento do cliente, shutdown sem conexões, request longo, stream upstream, deadline com handler travado e falha imediata ao abrir a porta. Todos os dados dos testes são sintéticos.

### Comandos e resultados reais

O shell usado não tinha Go no PATH e o cache padrão `C:\Users\Pichau\AppData\Local\go-build` recusou escrita com `Access is denied`. Os comandos Go abaixo foram executados sequencialmente no diretório `workspace/Virgil` após este preparo de sessão PowerShell:

```powershell
$env:PATH='C:\Program Files\Go\bin;C:\Users\Pichau\go\bin;'+$env:PATH
$env:GOCACHE=Join-Path $env:TEMP 'virgil-go-build-codex'
```

| Comando | Resultado em 2026-09-17 |
|---|---|
| `go version` | `go version go1.27.1 windows/amd64` |
| `gopls version` | `golang.org/x/tools/gopls v0.23.0` |
| `gofmt -l .` | Saída vazia, exit 0 |
| `go test ./...` | Oito pacotes passaram, exit 0 |
| `go vet ./...` | Saída vazia, exit 0 |
| `go build ./cmd/virgil` | Exit 0; emitiu aviso de `Access is denied` ao gravar o stat cache do módulo em `C:\Users\Pichau\go\pkg\mod\cache\download`. O executável local gerado foi removido após a verificação. |
| `go test ./tests/privacy` | Passou, exit 0 |
| `git diff --check` | Sem erros de whitespace, exit 0 |
| `go test -race ./...` | Exit 1: `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1` |
| `$env:CGO_ENABLED='1'; go test -race ./...` | Exit 1 antes dos testes: `cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in %PATH%` |
| `gopls check internal/gateway/server.go internal/gateway/chat.go` | Exit 0, sem diagnósticos, após apontar `LOCALAPPDATA` para cache temporário gravável |

Para reproduzir o check do gopls sem a falha de permissão no cache padrão:

```powershell
$env:LOCALAPPDATA=Join-Path $env:TEMP 'virgil-localappdata-codex'
New-Item -ItemType Directory -Force -Path $env:LOCALAPPDATA | Out-Null
gopls check internal/gateway/server.go internal/gateway/chat.go
```

`gcc`, `clang`, `cl.exe`, `golangci-lint`, `staticcheck` e `govulncheck` não foram encontrados no PATH. Por isso, `golangci-lint run ./...`, `staticcheck ./...` e `govulncheck ./...` não foram executados e não contam como verificações aprovadas. Após disponibilizar um compilador C compatível com Go no Windows, reproduzir com `$env:CGO_ENABLED='1'; go test -race ./...`. Nenhuma dependência foi adicionada; `go.mod` e `go.sum` não mudaram.

### Revisão de segurança do modo local

| Superfície | Estado verificado e risco restante |
|---|---|
| Bind | Configuração de entrada aceita somente IP de loopback; outros processos locais ainda podem acessar a porta. |
| Leitura e escrita HTTP | Headers têm 5 s, request completo 15 s e corpo de chat 1 MiB. Não há deadline global de escrita, para preservar SSE; cliente conectado que lê muito devagar ainda pode ocupar uma conexão. |
| Resposta do provedor | JSON tem limite total de 16 MiB; SSE limita cada linha e frame a 1 MiB, mas não o total da transmissão. O cliente HTTP padrão tem timeout de 60 s. |
| Redirecionamento e SSRF | Redirecionamentos automáticos do cliente do provedor são recusados. `base_url` aceita HTTP(S) configurado localmente e pode apontar para rede interna; o operador deve confiar no arquivo de configuração antes de fornecer uma chave. Política remota futura não deve poder ampliar esse destino sem validação. |
| Chaves e logs | Chave configurada vem de variável de ambiente e só é desbloqueada pelo token local separado. Respostas de erro e logs de eventos usam códigos fixos; prompts, respostas, argumentos e headers crus não são persistidos. O erro de startup pode incluir caminho local, mas não a chave. |
| SQLite e caminhos | Diretório novo é criado com modo 0700, WAL e foreign keys são ativados; a permissão efetiva de um arquivo ou diretório já existente depende do sistema operacional e do operador local. O caminho vem da configuração local. |
| SQL, outbox e retenção | Queries com dados variáveis usam parâmetros. Evento e outbox entram na mesma transação; retenção preserva eventos com entrega pendente. O pool tem uma conexão e a fila de escrita é serializada. O race detector ainda não validou concorrência em execução. |
| Contexto e shutdown | Contexto do request chega ao provedor; o sinal cancela requests e streams. Shutdown devolve erro de prazo e fecha conexões após cinco segundos. Um handler que ignore o cancelamento ainda precisa terminar seu próprio trabalho. |

Proteção necessária agora: manter o endpoint em loopback, a configuração local confiável, os limites de leitura e tamanho, a privacidade dos eventos e o shutdown com prazo. Requisito para um Control Plane futuro: validar destinos recebidos remotamente e impedir que política remota enfraqueça limites locais ou autorize captura de conteúdo. A ausência de `govulncheck` e do race detector impede afirmar ausência de vulnerabilidades de dependências ou corridas.

## Diagnóstico original — 2026-09-16

**Data:** 2026-09-16
**Escopo:** repositório workspace/Virgil
**Tipo:** auditoria de qualidade, segurança, testes e operação
**Skills aplicadas:** golang-how-to, golang-code-style, golang-lint, golang-testing, golang-security, golang-safety, golang-error-handling, golang-concurrency, golang-context e golang-database.

## Resumo

O núcleo atual está funcional: os testes normais e go vet passam. A revisão encontrou dois ajustes de confiabilidade/segurança no servidor HTTP e dois arquivos que precisam de gofmt.

O race detector, o gopls, golangci-lint, staticcheck e govulncheck ainda não foram concluídos porque o ambiente local não tinha todas as ferramentas configuradas e o cache de compilação apresentou erro de permissão.

## Achados

### P1 — leitura do corpo HTTP sem limite de tempo

**Arquivo:** internal/gateway/server.go
**Local:** configuração de http.Server em ListenAndServe.

O servidor define ReadHeaderTimeout, mas não define ReadTimeout. O handler usa http.MaxBytesReader, que limita o tamanho do corpo, porém não limita a velocidade de envio.

Um cliente que envia o corpo lentamente pode manter uma conexão ocupada por tempo indefinido. Isso cria risco de slowloris e consumo de conexões quando o gateway for exposto além do loopback.

**Correção necessária:** estabelecer um limite de leitura compatível com o contrato do proxy. A solução deve ser validada junto com SSE, porque um timeout global de escrita ou leitura não pode interromper streams legítimos.

**Teste recomendado:** abrir uma conexão que envia o corpo em partes lentas e confirmar encerramento dentro do prazo, além de confirmar que uma requisição SSE normal continua funcionando.

### P2 — shutdown sem prazo máximo

**Arquivo:** internal/gateway/server.go
**Local:** chamada srv.Shutdown(context.Background()).

O shutdown gracioso usa um contexto sem deadline. Uma conexão que não encerra pode fazer o processo esperar indefinidamente.

**Correção necessária:** criar um contexto de shutdown com prazo explícito, preservar o cancelamento e registrar o caso em que conexões não encerram a tempo.

**Teste recomendado:** iniciar uma requisição longa, cancelar o contexto principal e verificar que o servidor termina dentro do prazo configurado.

### P2 — arquivos fora do formato gofmt

Arquivos encontrados por gofmt -l:

- internal/redaction/event_test.go
- internal/storage/sqlite.go

As diferenças são apenas alinhamento de campos e ordem de imports. Não há indicação de mudança comportamental, mas o gate de formatação deve passar antes do próximo commit.

## Verificações executadas

### Passaram

    go test ./...
    go vet ./...

Pacotes cobertos:

- cmd/virgil
- internal/config
- internal/gateway
- internal/providers
- internal/redaction
- internal/storage
- internal/telemetry
- tests/privacy

### Pendentes por ambiente

    go test -race ./...

Falhou antes de executar os testes porque o ambiente estava com CGO desabilitado e sem compilador C disponível.

O gopls está instalado, mas a análise não carregou o workspace porque o cache em C:\Users\Pichau\AppData\Local\go-build retornou Access is denied.

As ferramentas abaixo não estavam disponíveis no PATH durante a revisão:

- golangci-lint
- staticcheck
- govulncheck

## Pontos positivos

- O listener padrão é validado como loopback.
- O tamanho do corpo de entrada é limitado.
- O tamanho da resposta do provedor é limitado.
- Redirecionamentos automáticos do cliente HTTP do provedor estão desabilitados.
- Credenciais configuradas precisam apontar para variáveis de ambiente.
- Eventos são validados antes da persistência.
- O SQLite usa WAL, foreign keys, transações e uma fila de escrita controlada.
- O contexto da requisição é propagado para o provedor.
- O registro do evento usa context.WithoutCancel com timeout próprio para não perder telemetria quando o cliente cancela.
- Os testes usam payloads e credenciais sintéticos.

## Ordem recomendada de correção

1. Corrigir os dois arquivos com gofmt.
2. Adicionar limite de leitura HTTP compatível com JSON e SSE.
3. Adicionar deadline ao shutdown gracioso.
4. Criar testes de regressão para timeout de leitura e shutdown.
5. Configurar compilador C e executar go test -race ./....
6. Executar golangci-lint, staticcheck e govulncheck.
7. Atualizar README, plano de implementação e vault com o resultado.

## Critério de conclusão

A etapa de qualidade só deve ser considerada concluída quando:

- gofmt -l não listar arquivos;
- go test ./... passar;
- go vet ./... passar;
- go test -race ./... passar ou tiver uma limitação de ambiente documentada;
- os novos testes de timeout e shutdown passarem;
- as ferramentas de análise estática e vulnerabilidades tiverem resultado registrado;
- o diff completo tiver sido revisado antes do checkpoint.
