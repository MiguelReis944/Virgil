# Memória do projeto

## Fatos e decisões

- Virgil está em beta local de desenvolvedor; o checklist de release ainda é necessário.
- O core funciona sem conta ou Control Plane e liga por padrão em loopback.
- O gateway é uma fronteira de observabilidade e guardrails, não um avaliador de outro
  LLM nem uma garantia de segurança factual.

## Armadilhas conhecidas

Um proxy só pode bloquear tráfego encaminhado por ele. Não prometer terminação de loops
externos e não registrar conteúdo completo por padrão; ambos os limites são parte do
contrato do produto.
