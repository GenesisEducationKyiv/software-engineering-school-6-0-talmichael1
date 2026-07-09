# rabbitmq_message_deduplication (+ its Elixir runtime) staged for RabbitMQ 3.13;
# enabled via the mounted enabled_plugins file.
FROM rabbitmq:3.13-management-alpine

ARG DEDUP_VERSION=0.6.4
ARG ELIXIR_EZ=elixir-1.16.3.ez
ARG PLUGINS_DIR=/opt/rabbitmq/plugins

RUN set -eux; \
    apk add --no-cache curl; \
    base="https://github.com/noxdafox/rabbitmq-message-deduplication/releases/download/${DEDUP_VERSION}"; \
    curl -fsSL -o "${PLUGINS_DIR}/${ELIXIR_EZ}" "${base}/${ELIXIR_EZ}"; \
    curl -fsSL -o "${PLUGINS_DIR}/rabbitmq_message_deduplication-${DEDUP_VERSION}.ez" \
         "${base}/rabbitmq_message_deduplication-${DEDUP_VERSION}.ez"
