# Coleção Postman: Wallet Service

Arquivos (esta pasta é ignorada pelo git):
- `wallet-service.postman_collection.json` (Collection v2.1, 11 pastas, 78 requisições)
- `wallet-local.postman_environment.json` (ambiente "Wallet Local")

## Como usar
1. Suba a stack (`docker compose up -d --wait`) e confira `http://localhost:8000/health/ready`.
2. Postman > Import > selecione os dois arquivos. No canto superior direito escolha o ambiente **Wallet Local**.
3. Rode `00 Autenticação` (opcional): a coleção tem um pre-request que busca e renova sozinho os tokens do provider-a, provider-b e wallet-internal (variáveis de coleção `tokenProviderA`, `tokenProviderB`, `tokenInternal`).
4. Ordem recomendada: 01 Health, 02 Carteira, 03 Rodada BET→WIN, 04 LOSS/REFUND/ROLLBACK, 05 Idempotência, 06 Referência pendente, 07 Autorização e isolamento, 08 SQS, 09 Observabilidade, 10 Erros de contrato.
5. Cada pasta é independente: a primeira requisição ("Setup") cria jogador e carteira novos, e os ids (playerId, walletId, externalTransactionId, transactionId, cursor) são gerados e encadeados por scripts. Dentro da pasta, rode de cima para baixo.

## Collection Runner (uma pasta por vez)
Clique com o botão direito na pasta > Run folder (ou Runner > arraste a pasta). Mantenha a ordem, deixe "Persist variables" desligado e, se quiser, ~300 ms de delay entre requisições. Cada requisição tem testes (`pm.test`); o resultado aparece na aba Test Results. As pastas 06 e 08 repetem uma consulta sozinhas (até 20 tentativas) via `setNextRequest`, o que só funciona no Runner/newman; enviando à mão, rode de novo após ~2 s.

Via linha de comando (sem instalar nada no projeto): `npx --yes newman run wallet-service.postman_collection.json -e wallet-local.postman_environment.json --delay-request 300`

## O que o Postman não faz
- **Concorrência real em paralelo** (mesma aposta 2x ao mesmo tempo, disputa de saldo, rajadas, queda de réplica): o Runner é sequencial. Use `scripts/scenarios/*.sh` (veja `scripts/scenarios/run-all.sh`).
- Ler **todos** os eventos de `wallet-events.fifo`: a fila tem milhares de mensagens antigas e a leitura devolve as 10 mais antigas; use o banco (`outbox_events`) ou o AWS CLI do compose.

## SQS pelo Postman (pasta 08)
Protocolo AWS JSON 1.0 (`X-Amz-Target: AmazonSQS.SendMessage|ReceiveMessage`), auth "AWS Signature" (service `sqs`, `us-east-1`). O MiniStack identifica o chamador pelo Access Key: `111111111111` (provider-a), `222222222222` (provider-b), `test` (dono, lê DLQ e eventos); o secret é ignorado. Funciona na prática, sem precisar do protocolo Query. A leitura da DLQ apaga só a mensagem de teste que encontrou.

## Problemas comuns
- **401 em tudo / "token expirado"**: o token dura 5 min; o pre-request renova sozinho. Se travou, limpe as variáveis de coleção `tokenProviderA/B/Internal` (e `...Exp`) e rode de novo, ou rode `00 Autenticação`. Confira que o Keycloak responde em `http://localhost:8080` (o issuer do token é esse endereço).
- **Erro de conexão / ECONNREFUSED**: stack fora do ar. `docker compose ps`, `docker compose up -d --wait`.
- **Pasta 08 falha ao achar mensagem na DLQ**: a DLQ pode ter mais de 10 mensagens antigas (a leitura traz 10). Limpe com `aws sqs purge-queue` (credencial root `test`) ou veja pelo AWS CLI do compose.
- **Pasta 09 sem dados de latência (p95 vazio)**: rode antes outras pastas; a janela é de 5 min.
- Pasta 07 "short-lived": espera 3,5 s no pre-request de propósito para o token de 2 s expirar.
