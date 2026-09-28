# Regras do projeto

## Invariantes

- Não persistir conteúdo de prompt, resposta ou argumentos de tool por padrão.
- Nunca expor credencial de provedor ao Control Plane ou confundir provider credential,
  app token e run token.
- Políticas devem ser determinísticas, explícitas e testáveis.
- Um circuit break encerra a árvore supervisionada correspondente; não matar processos
  arbitrários fora do proxy.

## Restrições

O gateway não observa chamadas diretas ao provedor, ferramentas fora dele ou loops
internos que não gerem nova chamada interceptada. O Control Plane não pode enfraquecer
limites locais.

## Regras de trabalho

Leia `README.md`, `CONTEXT.md`, contratos e `docs/release-acceptance.md` antes de mudar
protocolos, privacidade ou supervisão. Use exemplos sintéticos e nunca adicione chaves,
payloads reais ou endpoints privados.
