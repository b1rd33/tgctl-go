#!/usr/bin/env bash
set -euo pipefail

CHAT=1240314255
OUT=scripts/import_export_simulation.transcript.txt
REPORT=scripts/import_export_simulation_report.json
RUN_ID="ie-sim-$(date +%Y%m%d%H%M%S)"
RUN_SHORT="$(date +%H%M)"
TMP_DIR="/tmp/tgctl-${RUN_ID}"
DB="accounts/default/telegram.sqlite"
mkdir -p "$TMP_DIR"
> "$OUT"

log() {
  printf '%s\n' "$*" | tee -a "$OUT"
}

run_json_capture() {
  local dest="$1"
  shift
  log "+ $*"
  "$@" 2>&1 | tee "$dest" | tee -a "$OUT"
  jq -e '.ok == true' "$dest" >/dev/null
  log ""
}

run_json_summary() {
  local dest="$1"
  local summary_filter="$2"
  shift 2
  log "+ $*"
  "$@" >"$dest" 2>&1
  jq -e '.ok == true' "$dest" >/dev/null
  jq -c "$summary_filter" "$dest" | tee -a "$OUT" >/dev/null
  log ""
}

run_json_allow_error() {
  local dest="$1"
  local ok_filter="$2"
  shift 2
  log "+ $*"
  set +e
  "$@" >"$dest" 2>&1
  local status=$?
  set -e
  cat "$dest" | tee -a "$OUT"
  jq -e "$ok_filter" "$dest" >/dev/null
  log ""
  return 0
}

run_json_timeout_allow_error() {
  local seconds="$1"
  local dest="$2"
  local ok_filter="$3"
  shift 3
  log "+ timeout ${seconds}s $*"
  set +e
  perl -e 'my $t = shift @ARGV; alarm $t; exec @ARGV;' "$seconds" "$@" >"$dest" 2>&1
  local status=$?
  set -e
  if [ "$status" -eq 142 ] || [ "$status" -eq 14 ]; then
    printf '{"ok":false,"error":{"code":"TIMEOUT","message":"command timed out after %s seconds"}}\n' "$seconds" >"$dest"
  fi
  cat "$dest" | tee -a "$OUT"
  jq -e "$ok_filter" "$dest" >/dev/null
  log ""
}

send_inquiry() {
  local n="$1"
  local country="$2"
  local customer="$3"
  local product="$4"
  local order="$5"
  local intent="$6"
  local urgency="$7"
  local question="$8"
  local code
  code=$(printf 'SIM-IE-%03d' "$n")
  local text="${code} | run=${RUN_ID} | country=${country} | customer=${customer} | product=${product} | order=${order} | intent=${intent} | urgency=${urgency} | question=${question}"
  run_json_capture "$TMP_DIR/send-${code}.json" ./tg send "$CHAT" "$text" --allow-write --idempotency-key "${RUN_ID}-${code}" --json
}

log "=== import/export simulation start ==="
log "run_id: $RUN_ID"
log "self_chat: $CHAT"
log ""

run_json_capture "$TMP_DIR/version.json" ./tg version --json
run_json_capture "$TMP_DIR/me.json" ./tg me --json
run_json_capture "$TMP_DIR/backfill_entities.json" ./tg backfill-entities --json

send_inquiry 1 Germany "@sim_de_001" "olive oil" "PO-1001" "status" "high" "Can you confirm customs paperwork and ETA?"
send_inquiry 2 Germany "@sim_de_002" "machine parts" "PO-1002" "documents" "medium" "We need the packing list and certificate of origin."
send_inquiry 3 Germany "@sim_de_003" "coffee beans" "PO-1003" "pricing" "low" "Can you quote the next container at 20ft volume?"
send_inquiry 4 Germany "@sim_de_004" "medical gloves" "PO-1004" "status" "high" "The buyer is asking whether the shipment cleared Hamburg."
send_inquiry 5 Germany "@sim_de_005" "solar inverters" "PO-1005" "shipping" "medium" "Can you confirm vessel name and arrival window?"
send_inquiry 6 Spain "@sim_es_001" "ceramic tiles" "PO-2001" "pricing" "medium" "What is the MOQ for mixed pallets?"
send_inquiry 7 Spain "@sim_es_002" "wine bottles" "PO-2002" "documents" "high" "Please send the pro forma invoice today."
send_inquiry 8 Spain "@sim_es_003" "almonds" "PO-2003" "status" "low" "Do we have warehouse confirmation?"
send_inquiry 9 Spain "@sim_es_004" "textiles" "PO-2004" "shipping" "medium" "Can we split delivery across two addresses?"
send_inquiry 10 Spain "@sim_es_005" "olive oil" "PO-2005" "pricing" "high" "Customer wants revised payment terms before close of business."
send_inquiry 11 Italy "@sim_it_001" "marble slabs" "PO-3001" "shipping" "high" "The container is delayed; can you give a revised ETA?"
send_inquiry 12 Italy "@sim_it_002" "espresso machines" "PO-3002" "status" "medium" "Has production finished?"
send_inquiry 13 Italy "@sim_it_003" "leather goods" "PO-3003" "documents" "medium" "Need commercial invoice and HS codes."
send_inquiry 14 Italy "@sim_it_004" "tomato paste" "PO-3004" "pricing" "low" "Can you hold the current price for 14 days?"
send_inquiry 15 Italy "@sim_it_005" "furniture" "PO-3005" "shipping" "high" "Can you prioritize this order for air freight?"
send_inquiry 16 UAE "@sim_ae_001" "electronics" "PO-4001" "documents" "high" "Need export certificate and serial number list."
send_inquiry 17 UAE "@sim_ae_002" "perfume oils" "PO-4002" "pricing" "medium" "Can you quote CIF Dubai?"
send_inquiry 18 UAE "@sim_ae_003" "dates" "PO-4003" "status" "low" "Please confirm inspection booking."
send_inquiry 19 UAE "@sim_ae_004" "auto parts" "PO-4004" "shipping" "medium" "Buyer asks whether shipment can route through Jebel Ali."
send_inquiry 20 UAE "@sim_ae_005" "medical devices" "PO-4005" "documents" "high" "Please send compliance docs and batch certificate."

run_json_summary "$TMP_DIR/discover.json" '{ok,command,data:{discovered:.data.discovered}}' ./tg discover --allow-write --json
run_json_summary "$TMP_DIR/sync_contacts.json" '{ok,command,data:{synced:.data.synced}}' ./tg sync-contacts --allow-write --json
run_json_capture "$TMP_DIR/backfill.json" ./tg backfill "$CHAT" --max-messages 500 --allow-write --json
run_json_capture "$TMP_DIR/search.json" ./tg search "$CHAT" "$RUN_ID" --limit 25 --json

log "=== replies ==="
for country in Germany Spain Italy UAE; do
  case "$country" in
    Germany) reply="SIM-IE reply | run=${RUN_ID} | queue=Germany | action=Prioritize customs docs and ETA updates for five German inquiries." ;;
    Spain) reply="SIM-IE reply | run=${RUN_ID} | queue=Spain | action=Prepare MOQ/pricing answers and pro forma invoice follow-up." ;;
    Italy) reply="SIM-IE reply | run=${RUN_ID} | queue=Italy | action=Escalate shipping delays and invoice document requests." ;;
    UAE) reply="SIM-IE reply | run=${RUN_ID} | queue=UAE | action=Collect export certificates, compliance docs, and CIF Dubai quote." ;;
  esac
  run_json_capture "$TMP_DIR/reply-${country}.json" ./tg send "$CHAT" "$reply" --allow-write --idempotency-key "${RUN_ID}-reply-${country}" --json
done

GERMANY_REPLY_ID=$(jq -r '.data.message_id' "$TMP_DIR/reply-Germany.json")
run_json_capture "$TMP_DIR/pin.json" ./tg pin-msg "$CHAT" "$GERMANY_REPLY_ID" --allow-write --json
run_json_capture "$TMP_DIR/unpin.json" ./tg unpin-msg "$CHAT" "$GERMANY_REPLY_ID" --allow-write --json
run_json_capture "$TMP_DIR/mark_read.json" ./tg mark-read "$CHAT" --up-to "$GERMANY_REPLY_ID" --allow-write --json

log "=== temporary folders ==="
FOLDER_CHAT_IDS=()
while IFS= read -r folder_chat_id; do
  FOLDER_CHAT_IDS+=("$folder_chat_id")
done < <(python3 - "$DB" "$CHAT" <<'PY'
import sqlite3, sys
db_path, self_chat = sys.argv[1], int(sys.argv[2])
db = sqlite3.connect(db_path)
rows = db.execute("""
    SELECT chat_id
    FROM tg_chats
    WHERE chat_id != ?
      AND type IN ('channel', 'supergroup', 'group')
    ORDER BY CASE WHEN type = 'supergroup' THEN 0 WHEN type = 'group' THEN 1 ELSE 2 END, title
    LIMIT 4
""", (self_chat,)).fetchall()
for (chat_id,) in rows:
    print(chat_id)
PY
)
if [ "${#FOLDER_CHAT_IDS[@]}" -lt 4 ]; then
  log "not enough non-user dialogs for live folder membership; folder commands will use dry-run"
fi
idx=0
for country in Germany Spain Italy UAE; do
  case "$country" in
    Germany) folder_title="IE DE ${RUN_SHORT}" ;;
    Spain) folder_title="IE ES ${RUN_SHORT}" ;;
    Italy) folder_title="IE IT ${RUN_SHORT}" ;;
    UAE) folder_title="IE AE ${RUN_SHORT}" ;;
  esac
  if [ "${#FOLDER_CHAT_IDS[@]}" -ge 4 ]; then
    include_chat="${FOLDER_CHAT_IDS[$idx]}"
    idx=$((idx + 1))
    run_json_allow_error "$TMP_DIR/folder-create-${country}.json" '.ok == true or .error.code == "BAD_ARGS" or .error.code == "GENERIC"' \
      ./tg folder-create "$folder_title" --include-chats "$include_chat" --allow-write --idempotency-key "${RUN_ID}-folder-${country}" --json
  else
    run_json_capture "$TMP_DIR/folder-create-${country}.json" \
      ./tg folder-create "$folder_title" --include-chats "$CHAT" --allow-write --dry-run --idempotency-key "${RUN_ID}-folder-${country}" --json
  fi
done

log "=== forum topic mirror ==="
FORUM_CHAT=$(python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
rows = db.execute("""
    SELECT chat_id, title
    FROM tg_chats
    WHERE type = 'supergroup'
    ORDER BY CASE WHEN lower(title) LIKE '%forum%' THEN 0 ELSE 1 END, title
""").fetchall()
for chat_id, _title in rows:
    print(chat_id)
    break
PY
)
if [ -n "$FORUM_CHAT" ]; then
  run_json_timeout_allow_error 15 "$TMP_DIR/topics-list.json" '.ok == true or .error.code == "BAD_ARGS" or .error.code == "TIMEOUT" or .error.code == "GENERIC"' \
    ./tg topics-list "$FORUM_CHAT" --limit 10 --json
  if jq -e '.ok == true' "$TMP_DIR/topics-list.json" >/dev/null; then
    for country in Germany Spain Italy UAE; do
      case "$country" in
        Germany) topic_title="IE Germany - Status ${RUN_ID}" ;;
        Spain) topic_title="IE Spain - Pricing ${RUN_ID}" ;;
        Italy) topic_title="IE Italy - Shipping ${RUN_ID}" ;;
        UAE) topic_title="IE UAE - Documents ${RUN_ID}" ;;
      esac
      run_json_timeout_allow_error 15 "$TMP_DIR/topic-create-${country}.json" '.ok == true or .error.code == "BAD_ARGS" or .error.code == "TIMEOUT" or .error.code == "GENERIC"' \
        ./tg topic-create "$FORUM_CHAT" "$topic_title" --allow-write --idempotency-key "${RUN_ID}-topic-${country}" --json
      if jq -e '.ok == true' "$TMP_DIR/topic-create-${country}.json" >/dev/null; then
        topic_id=$(jq -r '.data.topic_id' "$TMP_DIR/topic-create-${country}.json")
        run_json_timeout_allow_error 15 "$TMP_DIR/topic-summary-${country}.json" '.ok == true or .error.code == "BAD_ARGS" or .error.code == "TIMEOUT" or .error.code == "GENERIC"' \
          ./tg send "$FORUM_CHAT" "SIM-IE summary | run=${RUN_ID} | country=${country} | inquiries=5 | source_chat=${CHAT}" --topic "$topic_id" --allow-write --idempotency-key "${RUN_ID}-topic-summary-${country}" --json
      fi
    done
  else
    log "forum target ${FORUM_CHAT} did not support live topic listing; topic mirror recorded as unavailable"
    for country in Germany Spain Italy UAE; do
      run_json_capture "$TMP_DIR/topic-dry-run-${country}.json" ./tg topic-create "$CHAT" "IE ${country} dry-run ${RUN_ID}" --allow-write --dry-run --json
    done
  fi
else
  log "no cached supergroup candidate; topic mirror recorded as unavailable"
  for country in Germany Spain Italy UAE; do
    run_json_capture "$TMP_DIR/topic-dry-run-${country}.json" ./tg topic-create "$CHAT" "IE ${country} dry-run ${RUN_ID}" --allow-write --dry-run --json
  done
fi

log "=== folder cleanup ==="
for country in Germany Spain Italy UAE; do
  file="$TMP_DIR/folder-create-${country}.json"
  if jq -e '.ok == true' "$file" >/dev/null; then
    folder_id=$(jq -r '.data.folder_id' "$file")
    run_json_allow_error "$TMP_DIR/folder-delete-${country}.json" '.ok == true or .error.code == "BAD_ARGS" or .error.code == "GENERIC"' \
      ./tg folder-delete "$folder_id" --allow-write --confirm "$folder_id" --json
  fi
done

python3 - "$RUN_ID" "$CHAT" "$DB" "$TMP_DIR" "$REPORT" <<'PY'
import glob
import json
import re
import sqlite3
import sys
from collections import Counter, defaultdict
from pathlib import Path

run_id, chat_id, db_path, tmp_dir, report_path = sys.argv[1], int(sys.argv[2]), sys.argv[3], Path(sys.argv[4]), sys.argv[5]
pattern = re.compile(r"(SIM-IE-\d{3}) \| run=([^|]+) \| country=([^|]+) \| customer=([^|]+) \| product=([^|]+) \| order=([^|]+) \| intent=([^|]+) \| urgency=([^|]+) \| question=(.*)")

db = sqlite3.connect(db_path)
rows = db.execute(
    """
    SELECT message_id, date, text
    FROM tg_messages
    WHERE chat_id = ? AND text LIKE ?
    ORDER BY message_id
    """,
    (chat_id, f"%run={run_id}%"),
).fetchall()

inquiries = []
replies = []
for message_id, date, text in rows:
    if not text:
        continue
    m = pattern.match(text)
    if m:
        code, _, country, customer, product, order, intent, urgency, question = m.groups()
        topic = {
            "Germany": "IE Germany - Status",
            "Spain": "IE Spain - Pricing",
            "Italy": "IE Italy - Shipping",
            "UAE": "IE UAE - Documents",
        }[country]
        inquiries.append({
            "code": code,
            "message_id": message_id,
            "date": date,
            "country": country,
            "customer": customer,
            "product": product,
            "order": order,
            "intent": intent,
            "urgency": urgency,
            "question": question,
            "recommended_folder": f"IE {country}",
            "recommended_topic": topic,
            "suggested_action": "reply_now" if urgency == "high" else "queue_follow_up",
        })
    elif text.startswith("SIM-IE reply |"):
        replies.append({"message_id": message_id, "date": date, "text": text})

for country in ["Germany", "Spain", "Italy", "UAE"]:
    reply = None
    reply_path = tmp_dir / f"reply-{country}.json"
    if reply_path.exists():
        try:
            reply = json.loads(reply_path.read_text())
        except json.JSONDecodeError:
            reply = None
    if reply and reply.get("ok"):
        replies.append({
            "message_id": reply.get("data", {}).get("message_id"),
            "date": None,
            "country": country,
            "text": reply.get("data", {}).get("text"),
        })

by_country = defaultdict(list)
for item in inquiries:
    by_country[item["country"]].append(item["code"])

def load_json(name):
    path = tmp_dir / name
    if not path.exists():
        return None
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError:
        return {"ok": False, "error": {"code": "UNPARSEABLE", "message": path.read_text()[:500]}}

folder_results = {}
for country in ["Germany", "Spain", "Italy", "UAE"]:
    create = load_json(f"folder-create-{country}.json")
    delete = load_json(f"folder-delete-{country}.json")
    folder_results[country] = {
        "create_ok": bool(create and create.get("ok")),
        "folder_id": create.get("data", {}).get("folder_id") if create else None,
        "cleanup_ok": bool(delete and delete.get("ok")) if create and create.get("ok") else None,
        "error": create.get("error") if create and not create.get("ok") else None,
    }

topic_results = {}
topics_list = load_json("topics-list.json")
for country in ["Germany", "Spain", "Italy", "UAE"]:
    create = load_json(f"topic-create-{country}.json")
    dry = load_json(f"topic-dry-run-{country}.json")
    summary = load_json(f"topic-summary-{country}.json")
    topic_results[country] = {
        "live_topic_created": bool(create and create.get("ok")),
        "topic_id": create.get("data", {}).get("topic_id") if create else None,
        "summary_sent": bool(summary and summary.get("ok")),
        "dry_run_used": bool(dry and dry.get("ok")),
        "error": (create or dry or {}).get("error"),
    }

report = {
    "run_id": run_id,
    "self_chat": chat_id,
    "summary": {
        "inquiries": len(inquiries),
        "replies": len(replies),
        "countries": dict(Counter(item["country"] for item in inquiries)),
        "intents": dict(Counter(item["intent"] for item in inquiries)),
        "urgent_count": sum(1 for item in inquiries if item["urgency"] == "high"),
    },
    "queues": {country: codes for country, codes in sorted(by_country.items())},
    "inquiries": inquiries,
    "replies": replies,
    "folders": folder_results,
    "topics": {
        "candidate_chat": None if not topics_list else topics_list.get("data", {}).get("chat", {}).get("chat_id"),
        "topics_list_ok": bool(topics_list and topics_list.get("ok")),
        "results": topic_results,
    },
    "capability_notes": [
        "Telegram folders organize chats/dialogs, not individual messages; this self-chat simulation can only model per-message folder assignment locally.",
        "Synthetic @sim_* customer handles are extracted from message text; they are not real Telegram usernames.",
        "Real topic creation requires a forum-enabled supergroup. If unavailable, the script records dry-run topic assignments.",
    ],
}

Path(report_path).write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")
print(json.dumps({"report": report_path, "inquiries": len(inquiries), "countries": report["summary"]["countries"]}, ensure_ascii=False))
PY

jq -e '.summary.inquiries == 20 and .summary.replies == 4 and (.queues | length) == 4 and ([.folders[] | .create_ok and .cleanup_ok] | all) and ([.topics.results[] | .live_topic_created and .summary_sent] | all)' "$REPORT" >/dev/null
log "report: $REPORT"
log "=== import/export simulation complete ==="
