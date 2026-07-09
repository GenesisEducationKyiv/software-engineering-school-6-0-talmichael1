# RabbitMQ + rabbitmq_message_deduplication.
#
# The plugin drops a repeat repo-check message (same x-deduplication-header)
# within the queue's cache TTL, so a duplicate enqueue never fans out a second
# batch of notification jobs — see internal/queue/conn.go (declareRepoChecks).
# The plugin is written in Elixir, so its runtime .ez ships alongside it. Pin the
# versions to the 0.6.4 release, which targets RabbitMQ 3.13 / Erlang 26.
FROM rabbitmq:3.13-management-alpine

ARG DEDUP_VERSION=0.6.4
ARG ELIXIR_EZ=elixir-1.16.3.ez
ARG PLUGINS_DIR=/opt/rabbitmq/plugins

RUN set -eux; \
    apk add --no-cache curl; \
    base="https://github.com/noxdafox/rabbitmq-message-deduplication/releases/download/${DEDUP_VERSION}"; \
    curl -fsSL -o "${PLUGINS_DIR}/${ELIXIR_EZ}" "${base}/${ELIXIR_EZ}"; \
    curl -fsSL -o "${PLUGINS_DIR}/rabbitmq_message_deduplication-${DEDUP_VERSION}.ez" \
         "${base}/rabbitmq_message_deduplication-${DEDUP_VERSION}.ez"; \
    rabbitmq-plugins enable --offline rabbitmq_message_deduplication
