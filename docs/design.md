# Design de interação

## Jornadas de uso

A pessoa inicia `virgil`, configura providers e proteções no painel local, executa um
filho supervisionado e inspeciona o resultado em Executions. Também pode consultar
Health, Settings e Usage após reiniciar o core.

## Interação e comportamento

Configuração ausente abre setup local; configuração inválida falha com mensagem
acionável. Uma policy block deve indicar motivo e estado terminal da execução. O painel
não deve sugerir que dados privados foram capturados quando a captura está desligada.

## Linguagem visual

O produto combina CLI e painel local. Providers, Protections, Executions, Health,
Settings e Usage são superfícies distintas; detalhes visuais continuam no painel e nos
designs existentes, enquanto este documento fixa a jornada e os estados.
