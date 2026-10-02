# backend-challenge-go

Serviço de carteiras e apostas distribuído em Go. Em construção.

- Enunciado: [docs/CHALLENGE.md](docs/CHALLENGE.md)
- Design: [docs/superpowers/specs/2026-10-01-wallet-service-design.md](docs/superpowers/specs/2026-10-01-wallet-service-design.md)

# Nota do desenvolvedor

*nota escrita manualmente.*

Este projeto, obviamente, foi feito 100% com agentes de código. Porém, não foi apenas jogado para ele fazer, mas sim com processos, ferramentas e metodologias voltadas para o desenvolvimento seguro e pratico com IA. 

Acredito veemente que atualmente não é mais pratico escrever código na mão (salvo para fix que a IA iria demorar mais que eu pra achar), sendo cético a tecnologia por um bom tempo desde que ela se provou apta para escrever 100% do código, então o processo de desenvolvimento deste projeto reflete o meu desenvolvimento dentro da empresa ao qual  entrego este desafio. 

Essa nota serve para eu como desenvolvedor relatar o que utilizei, como utilizei e que processos realizei no desenvolvimento.

Começando, foi lido 100% do enunciado, criterios de avaliação, ferramentas obrigatórias e recomendas e o objetivo esperado. Pessoalmente, nunca desenvolvi um ledger nem sabia a fundo como desenvolver um a nivel de codigo e segurança, apenas conhecia o conceito, entao antes mesmo de ir para o projeto, fui atras de pesquisar como um ledger era desenvolvido na pratica com a linguagem Go, assim saberia me guiar corretamente com a spec do agente.  
Após entender completamente o enunciado, parti para o agente, neste caso o Claude utilizado dentro da IDE Orca, onde utilizo o plugin \`superpowers\` para SDD, TDD, geração de planos mais definidos e subagent-driven-development.

No prompt inicial, marquei o antigo README (que agora é o [docs/CHALLENGE.MD)](docs/CHALLENGE.MD) e adicionei algumas informações, como diretivas especificas para não sair do contexto, seguir schemas e recomendações de tecnologia, focar na cobertura dos criterios de aceite e testabilidade, utilização do Gin como HTTP-server, optei por utilização do GORM com sql escrito, utilização do Nginx para facilitar uso de replicas do serviço, utilizar Prometheus com Grafana com graficos provisionados (não apenas logs), entre outros.  
Com isso e o agente em \`manual mode\`, ele vai me fazendo perguntas para decisões e gaps no proprio enunciado, para eu ir tomando decisoes e ir gerando o design inicial, e com isso o spike dos planos separados em etapas. Com a geração do design, é feito a revisão do design e aprovado para a geração do primeiro plano, que segue o mesmo fluxo até o final:  
Geração do plano -&gt; revisão do plano -&gt; aplicação com subagent-driven-development que aplica e faz revisoes a cada task dentro do plano -&gt; testes -&gt; revisão final -&gt; next   
Com isso, tenho o projeto 100% pronto, com ambiente testado em etapas e pronto para testes manuais, que realizo lendo o proprio enunciado e com ajuda do proprio Claude me passando cenarios e criando scripts para pentest de concorrencia.