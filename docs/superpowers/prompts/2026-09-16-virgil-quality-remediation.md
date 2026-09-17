# Prompt para o próximo chat — correção de qualidade do Virgil

Você está continuando o projeto Virgil.

## Diretório de trabalho

Use como raiz do harness:

D:\Arquivos\programação\harness

O repositório do Virgil é o submodule:

D:\Arquivos\programação\harness\workspace\Virgil

## Objetivo

Corrija os achados de qualidade registrados em:

workspace/Virgil/docs/quality/2026-09-16-quality-review.md

O objetivo desta etapa é deixar o Edge Gateway com qualidade verificável para continuar a evolução do projeto. Trabalhe em checkpoints pequenos. Ao terminar cada checkpoint, valide os testes, faça um commit local no repositório correto e informe o hash, os arquivos e os resultados. Push continua proibido, a menos que o usuário peça explicitamente.

## Skills obrigatórias

Antes de ler e alterar o código, carregue e aplique:

- golang-how-to
- golang-code-style
- golang-lint
- golang-testing
- golang-security
- golang-safety
- golang-error-handling
- golang-concurrency
- golang-context
- golang-database
- golang-gopls
- golang-troubleshooting se algum teste ou ferramenta falhar
- golang-dependency-management se alguma dependência precisar ser adicionada
- ponytail para evitar abstrações e dependências desnecessárias

Carregue as skills como um conjunto. Não adicione bibliotecas apenas para resolver um problema que a biblioteca padrão já resolve.

## Regras do repositório

1. Leia o AGENTS.md da raiz do harness.
2. Leia vault/AGENTS.md.
3. Leia vault/projects/Virgil/_context.md.
4. Leia workspace/Virgil/README.md.
5. Leia workspace/Virgil/docs/design/2026-09-16-virgil-architecture.md.
6. Leia workspace/Virgil/docs/superpowers/plans/2026-09-16-virgil-implementation-plan.md.
7. Pode fazer commits locais nos checkpoints concluídos.
8. Não faça push, force push ou publicação sem pedido explícito do usuário.
9. Não use git reset --hard, git clean ou outra operação destrutiva.
10. Não reverta trabalho existente.
11. Toda mudança de código dentro de workspace/Virgil pertence ao Git do submodule.
12. Preserve a privacidade local: não adicione captura de prompts, respostas, argumentos de ferramentas, headers crus ou credenciais aos eventos.
13. Use apenas dados sintéticos nos testes.

## Estado conhecido

O Virgil já possui:

- servidor HTTP local;
- health check;
- proxy OpenAI-compatible;
- JSON e SSE;
- cancelamento de requisição;
- IDs de trace;
- eventos canônicos;
- redaction e scanner de privacidade;
- journal SQLite;
- migrações;
- WAL;
- outbox;
- retenção;
- identidade persistente da instalação.

## Etapa 0 — diagnóstico inicial

Antes de editar:

1. Verifique o estado Git do harness e do submodule.
2. Confirme versões de go e gopls.
3. Confirme se gcc, clang ou cl.exe estão disponíveis para o race detector.
4. Confirme o diretório de cache Go e suas permissões.
5. Verifique se existem golangci-lint, staticcheck e govulncheck.
6. Execute os testes sem concorrência entre comandos:

    go test ./...
    go vet ./...
    gofmt -l .

Não execute vários comandos Go simultaneamente no mesmo módulo, pois eles podem disputar o cache de compilação.

## Etapa 1 — formatação

Corrija somente a formatação de:

- internal/redaction/event_test.go
- internal/storage/sqlite.go

Use gofmt. Depois execute:

    gofmt -l .
    go test ./...
    go vet ./...

Não misture correções funcionais nesta etapa.

### Checkpoint 1

Pare e informe:

- arquivos alterados;
- saída de gofmt -l .;
- saída de go test ./...;
- saída de go vet ./...;
- hash do commit local realizado e mensagem usada.

## Etapa 2 — timeout de leitura HTTP

Revise internal/gateway/server.go e internal/gateway/chat.go.

O problema é que ReadHeaderTimeout limita apenas os headers. O corpo pode ser enviado lentamente sem prazo máximo.

Implemente a menor solução segura que preserve:

- JSON normal;
- requests dentro de maxChatRequest;
- streaming SSE;
- cancelamento pelo cliente;
- propagação de contexto ao provedor;
- mensagens de erro sem vazamento de detalhes.

Não escolha um WriteTimeout global curto sem testar streams longos. Se a solução usar configuração, mantenha o default seguro e não crie abstração de configuração sem necessidade real.

Adicione testes de regressão para:

1. corpo enviado lentamente e encerrado pelo timeout;
2. corpo acima do limite;
3. JSON normal ainda funcionando;
4. SSE ainda entregando o primeiro frame antes do fim do provedor;
5. cancelamento ainda cancelando a chamada upstream.

Use httptest e dados sintéticos.

### Checkpoint 2

Execute:

    gofmt -w <arquivos-alterados>
    go test ./...
    go vet ./...

Depois pare e informe o resultado completo antes de seguir.

## Etapa 3 — shutdown com deadline

Revise ListenAndServe em internal/gateway/server.go.

O código atual usa srv.Shutdown(context.Background()), que pode esperar indefinidamente.

Implemente um shutdown com prazo explícito, preservando:

- encerramento gracioso;
- cancelamento de requests ativos;
- encerramento de streams quando o processo recebe sinal;
- retorno de erro do servidor;
- ausência de goroutine vazando.

Não introduza framework de lifecycle ou container DI. Use context.WithTimeout e a biblioteca padrão, a menos que os testes demonstrem uma necessidade diferente.

Adicione testes para:

1. shutdown sem conexões pendentes;
2. shutdown com uma conexão longa;
3. término dentro do prazo;
4. encerramento da goroutine do servidor;
5. comportamento quando ListenAndServe falha imediatamente.

### Checkpoint 3

Execute:

    gofmt -w <arquivos-alterados>
    go test ./...
    go vet ./...

Pare para o checkpoint do usuário.

## Etapa 4 — concorrência, race e ferramentas

Depois das correções funcionais:

1. Rode go test -race ./... de forma isolada.
2. Se falhar por falta de CGO, identifique o compilador necessário e documente a limitação exata.
3. Rode golangci-lint run ./..., se instalado.
4. Rode staticcheck ./..., se instalado.
5. Rode govulncheck ./..., se instalado.
6. Rode gopls com o PATH correto e confirme que o workspace carrega.
7. Não trate ferramenta ausente como sucesso.
8. Não instale dependências sem mostrar o motivo, a versão e o impacto em go.mod/go.sum.

Se o ambiente impedir uma ferramenta, registre:

- comando;
- erro literal;
- se é problema do código ou do ambiente;
- comando manual para reproduzir depois.

## Etapa 5 — revisão de segurança

Use golang-security e revise:

- bind de rede;
- timeout de leitura e escrita;
- limites de request e response;
- redirecionamentos HTTP;
- SSRF por base_url configurável;
- vazamento de API keys;
- logs de dados sensíveis;
- persistência do SQLite;
- caminhos de arquivo;
- SQL parametrizado;
- propagação de contexto;
- tratamento de erros;
- shutdown;
- corrida entre journal, outbox e retenção.

Não transforme riscos futuros de enterprise em implementação especulativa nesta etapa. Separe:

- risco existente no modo local;
- proteção necessária agora;
- requisito para o futuro Control Plane.

## Etapa 6 — documentação e vault

Se o comportamento mudar:

1. Atualize o README somente com o estado implementado.
2. Atualize o plano se uma tarefa foi concluída ou alterada.
3. Atualize o relatório de qualidade com o resultado.
4. Atualize o subwiki em vault/projects/Virgil/ seguindo vault/AGENTS.md.
5. Registre comandos e resultados reais.

Não descreva como resolvido algo que foi apenas analisado.

## Formato obrigatório da resposta em cada checkpoint

### O que foi feito

Explique em linguagem simples.

### Arquivos alterados

Liste cada arquivo e o motivo.

### Testes executados

Mostre os comandos exatos e os resultados.

### Segurança

Diga qual risco foi reduzido e qual risco continua.

### Pendências

Liste somente o que ainda falta.

### Commit checkpoint

Informe o commit local realizado, seu hash e a mensagem usada. Se a etapa não estiver pronta, não faça commit e explique o bloqueio.

## Critério final

Só declare a etapa concluída quando:

- gofmt -l . não listar arquivos;
- go test ./... passar;
- go vet ./... passar;
- timeout de leitura tiver teste de regressão;
- shutdown tiver deadline e teste de regressão;
- race detector e ferramentas de segurança tiverem resultado documentado;
- documentação e vault estiverem coerentes com o código;
- o diff completo tiver sido revisado.
