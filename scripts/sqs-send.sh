#!/usr/bin/env bash
# Sends a message to wager-transactions.fifo as a provider, using the AWS CLI
# image of the compose project and the provisioned credentials file.
# Usage: scripts/sqs-send.sh PROFILE MESSAGE_GROUP_ID 'JSON BODY' [DEDUP_ID]
# The body's messageId is used as MessageDeduplicationId (producer contract) unless DEDUP_ID is given
# (useful to send the same message again past the FIFO deduplication window).
set -euo pipefail
cd "$(dirname "$0")/.."
profile=${1:?profile} group=${2:?group} body=${3:?body}
dedup=${4:-$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["messageId"])' "$body")}
docker compose run --rm --no-deps -T \
  -e AWS_SHARED_CREDENTIALS_FILE=/out/credentials -e AWS_PROFILE="$profile" -e AWS_DEFAULT_REGION=us-east-1 \
  --entrypoint aws ministack_init --endpoint-url http://ministack:4566 \
  sqs send-message --queue-url http://ministack:4566/000000000000/wager-transactions.fifo \
  --message-group-id "$group" --message-deduplication-id "$dedup" --message-body "$body" --output text --query MessageId
