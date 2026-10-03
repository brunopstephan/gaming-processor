#!/usr/bin/env bash
# Roda os cenarios 01..10 e o db-check; imprime tabela-resumo. Logs em .local/scenario-logs/.
# Requer a stack no ar (docker compose up --build -d), curl e python3.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
logs="$here/../../.local/scenario-logs"; mkdir -p "$logs"
rows=(); rc=0
for s in "$here"/[0-9][0-9]-*.sh "$here"/db-check.sh; do
  name=$(basename "$s" .sh)
  echo ">>> $name"
  start=$(date +%s)
  if "$s" 2>&1 | tee "$logs/$name.log"; then res=OK; else res=FALHOU; rc=1; fi
  [ "${PIPESTATUS[0]}" -eq 0 ] || { res=FALHOU; rc=1; }
  rows+=("$(printf '%-36s %-8s %ss' "$name" "$res" "$(( $(date +%s) - start ))")")
done
echo; echo "================ RESUMO ================"
printf '%-36s %-8s %s\n' "cenario" "resultado" "duracao"
printf '%s\n' "${rows[@]}"
exit $rc
