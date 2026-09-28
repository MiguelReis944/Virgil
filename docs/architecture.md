# Arquitetura atual

## Componentes e limites

O binário Go inicia o core local, configuração e painel. O gateway recebe chamadas,
aplica políticas e adapta provedores; o tracker associa execução, run token e processo;
SQLite registra eventos e estado; redaction e exporters controlam saída; Control Plane é
opcional e separado.

## Fluxos de dados e controle

Cliente → gateway local → adapter do provedor. O runner inicia uma árvore de processo,
injeta endereço e credencial locais, e o breaker encerra a árvore quando uma política,
deadline ou interrupção dispara. Exportação usa outbox e formatos configurados.

## Interfaces

As interfaces públicas incluem `/health`, `/v1/chat/completions`, `/v1/tool-results`,
configuração TOML, schemas OpenAPI e o painel local. A credencial do provider fica na
borda; run token e app token são identidades distintas.

## Decisões técnicas vigentes

O produto é local-first, provider-neutral, privacy-by-default e determinístico. O
gateway só governa tráfego que passa por ele; controle de ciclo de vida completo requer
Runner ou SDK. Designs datados em `docs/design/` são fontes de decisão, não cópia deste
resumo.
