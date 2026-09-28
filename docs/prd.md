# Requisitos do produto

## Problema e pessoas usuárias

Agentes locais podem repetir chamadas, exceder orçamento ou continuar executando sem
limites observáveis. Virgil oferece um gateway local que registra sinais úteis, aplica
políticas determinísticas e interrompe execuções supervisionadas.

## Escopo atual

Virgil é um gateway local em beta de desenvolvedor, com adaptadores de provedores,
políticas, histórico SQLite, exportação JSONL, painel local e modo Runner opcional.

## Requisitos

- Encaminhar protocolos OpenAI-compatible e o subconjunto suportado de Anthropic/Kimi.
- Enforcear limites, deadlines e bloqueios por repetição de erros de modo determinístico.
- Supervisionar uma árvore de processos com identidade e run token estáveis.
- Preservar privacidade por padrão: prompts, respostas e argumentos não são persistidos
  ou exportados sem configuração explícita.
- Funcionar sem conta ou Control Plane; integração remota deve ser opcional.

## Critérios de aceitação e evidências

`README.md`, contratos em `schemas/`, `docs/protocol/` e
`docs/release-acceptance.md` são as evidências atuais. O checklist de release continua
obrigatório antes de chamar o produto de finalizado.

## Fora de escopo

Treinar modelos, substituir provedores, inspecionar raciocínio oculto, garantir correção
factual, exigir cloud ou observar loops que nunca passam pelo gateway estão fora do
primeiro produto.
