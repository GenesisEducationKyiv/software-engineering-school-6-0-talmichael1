#!/bin/sh
set -eu

KIBANA="${KIBANA_URL:-http://kibana:5601}"
DATA_VIEW_ID="app-logs"
SAVED_SEARCH_ID="app-logs-default"

until curl -sf "${KIBANA}/api/status" >/dev/null; do
  echo "waiting for kibana..."
  sleep 2
done

dv_status=$(curl -s -o /dev/null -w "%{http_code}" "${KIBANA}/api/data_views/data_view/${DATA_VIEW_ID}")
if [ "${dv_status}" = "200" ]; then
  echo "data view ${DATA_VIEW_ID} already exists"
else
  curl -sS --fail -X POST "${KIBANA}/api/data_views/data_view" \
    -H "kbn-xsrf: true" \
    -H "Content-Type: application/json" \
    -d "{\"data_view\":{\"id\":\"${DATA_VIEW_ID}\",\"title\":\"app-logs-*\",\"name\":\"app-logs\",\"timeFieldName\":\"@timestamp\"}}" >/dev/null
  echo "data view ${DATA_VIEW_ID} created"
fi

ss_status=$(curl -s -o /dev/null -w "%{http_code}" "${KIBANA}/api/saved_objects/search/${SAVED_SEARCH_ID}")
if [ "${ss_status}" = "200" ]; then
  echo "saved search ${SAVED_SEARCH_ID} already exists"
  exit 0
fi

curl -sS --fail -X POST "${KIBANA}/api/saved_objects/search/${SAVED_SEARCH_ID}" \
  -H "kbn-xsrf: true" \
  -H "Content-Type: application/json" \
  -d '{
    "attributes": {
      "title": "App Logs",
      "description": "Application logs with curated default columns",
      "columns": ["level", "msg", "method", "path", "status", "duration", "error", "trace_id"],
      "sort": [["@timestamp", "desc"]],
      "kibanaSavedObjectMeta": {
        "searchSourceJSON": "{\"query\":{\"query\":\"\",\"language\":\"kuery\"},\"filter\":[],\"indexRefName\":\"kibanaSavedObjectMeta.searchSourceJSON.index\"}"
      }
    },
    "references": [
      {"id": "app-logs", "name": "kibanaSavedObjectMeta.searchSourceJSON.index", "type": "index-pattern"}
    ]
  }' >/dev/null
echo "saved search ${SAVED_SEARCH_ID} created"
