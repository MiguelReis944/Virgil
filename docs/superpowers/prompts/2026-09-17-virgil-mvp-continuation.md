# Prompt para o próximo chat — concluir o MVP do Virgil

Você está continuando o Virgil. Execute o trabalho, em vez de apenas propor outro plano. Avance por checkpoints verificáveis até concluir o MVP local do Edge Gateway e preparar a integração com o Control Plane conforme as decisões já registradas. Se o escopo final exigir o serviço de equipe, trate-o como uma entrega separada, no repositório correto.

## Onde trabalhar e o que ler

- Harness: `D:\Arquivos\programação\harness`.
- Repositório Git independente do Virgil: `D:\Arquivos\programação\harness\workspace\Virgil`.
- Leia `AGENTS.md` na raiz do harness, as instruções aplicáveis do vault, `vault/projects/Virgil/_context.md`, o `README.md` do Virgil, `docs/design/2026-09-16-virgil-architecture.md`, `docs/superpowers/plans/2026-09-16-virgil-implementation-plan.md` e `docs/quality/2026-09-16-quality-review.md`.
- Use `using-superpowers`, `repo-orchestrator` e `golang-how-to` para escolher as skills pertinentes. Para cada etapa, aplique as skills Go de teste, segurança, concorrência, contexto, banco, estilo e erros quando forem relevantes; use `systematic-debugging` quando uma verificação falhar. Use `ponytail` para manter o menor desenho que satisfaça os requisitos. Use as skills de revisão e verificação antes de declarar um checkpoint pronto.
- O plano existente contém detalhes de arquivos, interfaces e testes de cada tarefa. Trate-o como especificação inicial; confirme tudo no código atual antes de editar. Não recrie funcionalidade já implementada.

## Estado observado em 2026-09-17

O `main` local do Virgil está em `d7eef4f` (`Implement guardrails and policy engine for request management`), igual a `origin/main` no momento da inspeção. A árvore de trabalho estava limpa antes deste prompt. Desde `39b0657`, houve formatação, revisão de qualidade, prazo de leitura do request completo, shutdown com prazo e a primeira implementação do motor de políticas. O commit `d7eef4f` adicionou a configuração de guardrails, `internal/policies`, contadores e reservas transacionais no SQLite, migração `003_policy_counters.sql`, schema da política e integração de `Preflight`/`Postflight` ao gateway. Os testes cobrem limites e concorrência do orçamento de chamadas, bloqueios e reconciliação. Isso representa avanço substancial na Task 7; **audite sua aceitação antes de marcá-la concluída**.

O gateway já inicia localmente, oferece health check, encaminha Chat Completions JSON/SSE, registra eventos canônicos no SQLite e faz redaction. O servidor limita headers a 5 s, leitura completa a 15 s e corpo de chat a 1 MiB; o shutdown tem prazo de 5 s. O README e o plano ainda descrevem redaction/políticas ou a Task 7 como futuros; corrija essa documentação conforme a implementação real. `configs/virgil.example.toml` ainda não mostra `[guardrails]`. Não há implementação das Tasks 8–14 no repositório, nem os módulos de exportação, pricing, cliente do Control Plane, contratos e e2e previstos no plano. Tasks 15–18 pertencem a **outro repositório**. Runner e SDK (19–20) são posteriores ao Edge estável.

Nesta inspeção, `go test ./...` passou em nove pacotes e `go vet ./...` passou, usando um `GOCACHE` temporário. O cache padrão em `C:\Users\Pichau\AppData\Local\go-build` recusou escrita. O Go existe em `C:\Program Files\Go\bin\go.exe`, embora não estivesse no `PATH` do shell. A revisão anterior documenta que `go test -race` não rodou porque faltava compilador C/CGO; reavalie o ambiente, mas nunca apresente essa verificação como aprovada sem executá-la.

## Objetivo e definição de MVP

Há duas definições documentais que precisam ser reconciliadas. O README chama de primeiro marco o gateway local que funciona sem Docker, conta tokens/latência, bloqueia o quarto erro repetido e persiste eventos offline. O fechamento do plano exige também OTLP, precedência de política remota, agregação do serviço e revogação de instalação. Logo, distinga explicitamente:

1. **MVP local do Edge Gateway:** recursos locais, privacidade, testes end-to-end, instalação e documentação demonstráveis sem conta, nuvem ou Control Plane.
2. **MVP integrado:** Edge + contrato público + serviço de equipe em repositório separado, com ingestão, agregação, revogação e política remota demonstradas de ponta a ponta.

No primeiro checkpoint, ajuste README/plano para deixar esses critérios honestos, sem rebaixar um requisito silenciosamente. Em seguida, implemente as Tasks 8–14 do Edge em ordem de dependência. Se o usuário já tiver definido o repositório e o modelo de propriedade/licença/deploy do Control Plane, continue pelas Tasks 15–18 nele. Se esses dados ainda não existirem, conclua o Edge e o contrato até a Task 14, entregue uma demonstração verificável e peça **apenas** a decisão concreta necessária para iniciar o serviço. Não crie o serviço dentro do repositório público do Virgil e não declare o MVP integrado pronto antes dos testes do serviço.

## Regras de execução

- Você pode fazer **commits locais** para checkpoints coesos e testados no Git do Virgil. Registre hash, escopo e testes de cada um. Não faça push, publicação ou automação que publique sem pedido explícito. Não use `reset --hard`, `clean` nem reverta mudanças alheias. Depois de commits no submodule, atualize o ponteiro do harness em commit separado somente quando for apropriado e autorizado pelas instruções atuais. Nunca commite código do submodule na raiz do harness.
- Revise no início e atualize as regras antigas do plano que dizem que só o humano pode commitar e que agentes não podem delegar; elas conflitam com a autorização posterior para commits locais. Preserve a proibição de push. Subagentes podem ajudar em subtarefas independentes e delimitadas, se disponíveis; cada agente deve ter responsabilidade explícita sobre arquivos e não sobrescrever edições de outros.
- Faça testes que falhem pelo comportamento ausente antes da implementação, quando a mudança for de código. Um checkpoint só termina com teste focado, `go test ./...`, `go vet ./...`, `go build ./cmd/virgil`, `go test ./tests/privacy`, revisão do diff, `git diff --check` e status. Se um comando falhar pelo ambiente, documente a causa e uma execução alternativa segura; se falhar pelo código, corrija antes de avançar.
- Não introduza conteúdo sensível em telemetria, logs, JSONL ou exportação: prompts, respostas, argumentos/resultados de tools, headers crus, chaves, tokens e Chain of Thought ficam fora. Use somente dados sintéticos e canários nos testes. Exportação exige destino e lista explícita de campos. A falha de exportação nunca interrompe o proxy local. Não faça retry automático de requests ao provedor.
- Mantenha o gateway em loopback por padrão, funcional offline e sem serviço externo obrigatório. Preserve JSON/SSE, cancelamento, trace IDs e os limites HTTP já corrigidos. Adicione dependências somente quando necessárias e com versões fixadas; não crie abstrações ou serviços sem um teste de aceitação que os justifique.

## Sequência de checkpoints

### 0. Baseline e alinhamento

Confirme HEAD, remotos, status dos dois repositórios e estado real das Tasks 1–7. Examine especificamente se o motor de políticas faz reserva atômica, registra um evento por bloqueio e reconcilia contadores após erro, cancelamento e stream; teste restart e concorrência onde houver risco real. Verifique o efeito de custo desconhecido (`cost_unavailable`) e da configuração de estimativas. Se encontrar defeito, corrija-o antes da Task 8. Atualize README, exemplo de configuração, plano e contexto do projeto para refletir o que já funciona e as duas definições de MVP. Registre qualquer limitação atual de race detector sem mascará-la.

### 1. Task 8 — erros repetidos e feedback de tools

Implemente `POST /v1/tool-results` com autenticação pelo token da aplicação local em header dedicado; sem token configurado, a rota fica desativada. Aceite somente metadados delimitados (`run_id`, `tool_call_id`, nome, status, código normalizado), com tamanho e valores validados. Não aceite corpo de resultado, argumentos ou texto livre. Conte três falhas equivalentes e bloqueie localmente a quarta tentativa com `repeated_tool_error`, sem chamar o provedor. Erros idênticos do provedor, visíveis ao proxy, também entram na detecção. Faça sequência diferente reiniciar a contagem conforme a especificação. Persistência/restart da sequência devem ser decididos a partir do requisito, não presumidos. Demonstre que o gateway não afirma ter encerrado um processo que ele não supervisiona. Cubra autenticação, privacidade, concorrência e a transição terceira → quarta chamada.

### 2. Task 9 — provedores

Complete o registro de adapters/configuração para OpenAI, Kimi e endpoint OpenAI-compatible genérico, mantendo URL, modelo, chave por ambiente e capacidades configuráveis. Adicione o subset Anthropic Messages com tradução JSON, SSE, tool use, uso e erros, rejeitando capacidades sem suporte **antes** da chamada de rede. Não fixe preço, modelo ou endpoint no roteador central. Use servidores falsos e fixtures sintéticas; comprove cancelamento e ausência de vazamento de credenciais.

### 3. Task 10 — uso e custo auditáveis

Modele a origem dos tokens como `provider`, `estimated` ou `unknown`. Tokens de cache são subconjunto do input e não somam novamente. Crie tabela de preço configurável, com versão e moeda, e cálculo decimal. Preencha custo real somente quando houver uso informado pelo provedor e preço conhecido; estimativa só quando uso estimado e preço conhecido; caso contrário, mantenha custo nulo. Registre versão e entradas de cálculo no evento, descreva que custo calculado não equivale a fatura do provedor e integre estimativas com os limites da política sem permitir que custo desconhecido atravesse um hard cap.

### 4. Task 11 — outbox recuperável e JSONL local

Complete `Lease`, `Ack` e `Fail` transacionais por destino, expiração de lease após restart, retry com backoff/jitter limitado e dead letter. Ack só dos IDs aceitos, sem apagar evento por falha de um destino. Adicione paginação limitada do journal e CLI `virgil events export --format jsonl --output <path>`, com caminho explícito, lista de campos permitidos, IDs estáveis e uma linha canônica redigida por evento. O comando deve funcionar sem rede. Teste restart, múltiplos destinos, falha permanente, paginação, privacidade e arquivo incompleto.

### 5. Task 12 — OTLP/HTTP

Envie traces protobuf válidos a `/v1/traces`, com trace/span ID, status, modelo/provedor, latência, tokens, proveniência e custos permitidos. Aplique a allowlist e redaction novamente na borda da exportação. Faça retry apenas de telemetria para 429/502/503/504, respeitando `Retry-After`; 4xx permanente vai a dead letter. Um collector local falso deve decodificar o payload, simular queda/reconexão e provar ausência de canários e continuidade do proxy offline.

### 6. Task 13 — cliente opcional do Control Plane

Implemente enrollment com token de uso único e credencial escopada guardada em secret store ou arquivo local ignorado explicitamente configurado. Batch deve confirmar apenas eventos aceitos. Faça cache de última política válida com versão monotônica/ETag; política remota só pode tornar limites locais mais estritos, nunca alterar destino/exportação/captura. Com endpoint offline, política local e gravação continuam. Credencial revogada interrompe apenas acesso remoto. Teste tudo com TLS local e identidades sintéticas.

### 7. Task 14 — contrato público e suíte de compatibilidade

Publique OpenAPI v1 e exemplos de política para enrollment, batch, política atual e revogação administrativa. Documente respostas com IDs aceitos e rejeições por evento, derivação de tenant pela credencial e deduplicação por organização/instalação/evento. Valide o schema e teste compatibilidade contra servidor falso sem ler tipos internos. Monte e2e que inicie o binário local, use provedores/collector falsos e prove JSON, SSE, Kimi por configuração, trace, limite do quarto erro, privacidade, evento offline, reconexão e OTLP decodificável. README deve permitir a uma pessoa reproduzir essa demonstração sem credenciais reais.

### 8. Fechamento do Edge e porta para o serviço

Execute a suíte completa, teste de corrida se o toolchain suportar CGO, cross-build para Windows amd64, macOS arm64 e Linux amd64, scan sintético de privacidade e testes de contrato/e2e. Inspecione binários gerados e mantenha-os fora do Git. Faça uma matriz requisito → evidência → resultado, marcando explicitamente `passou`, `falhou` ou `bloqueado pelo ambiente`. Corrija falhas do código. Atualize README, plano e vault com fatos testados. Se tudo do Edge passar, declare **Edge MVP concluído**, sem confundir isso com o MVP integrado.

### 9. MVP integrado, se o repositório do serviço já estiver decidido

Execute as Tasks 15–18 no **repositório separado**: ingestão PostgreSQL por instalação/tenant com credential hash e idempotência; agregação e retenção isoladas por tenant; revogação; interface de custo/erros; alertas, auditoria, distribuição de políticas; RBAC e OIDC. Use dois tenants sintéticos em testes de isolamento. SAML exige revisão específica de segurança e é uma porta enterprise, não um atalho para fechar o MVP. Integre ao contrato da Task 14 e prove agregação total/individual, revogação efetiva e política remota mais estrita. Se a decisão sobre o repositório não existir, pare **somente esta etapa**, apresente opções concretas de repositório/propriedade/licença/deploy e a recomendação técnica, sem levar o código do serviço ao Virgil público.

## Critério de encerramento e formato de resposta

Não declare o MVP integrado completo sem evidência e2e do serviço. Runner (Task 19) e SDK Python (Task 20) ficam para depois do Edge estável, salvo decisão explícita de escopo; registre essa escolha. A cada checkpoint, informe o que mudou, por quê, testes com resultados reais, riscos remanescentes, commit local e próximo passo. Ao final, entregue uma tabela curta com estado das Tasks 7–20 e a matriz de aceitação do Edge e do serviço. Cite hashes de commits e arquivos relevantes. Não faça push.
