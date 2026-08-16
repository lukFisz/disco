#!/bin/sh
# Triggers a Home Assistant automation via its REST API.
# Usage: ha_trigger_automation.sh <object_id> <channel_id>
#   object_id is the automation's entity_id without the "automation."
#   domain prefix, e.g. "evening_lights" for entity_id automation.evening_lights.
#   channel_id is the Discord channel the invoking command was run in
#   (disco's built-in {channel.id} placeholder - see README.md), passed
#   through to HA as a trigger variable so the automation knows where to
#   send a notification back to.
#
# Requires HA_URL (e.g. http://homeassistant.local:8123, no trailing
# slash needed) and HA_TOKEN (a Long-Lived Access Token from HA's
# Profile -> Security page) in the environment. See README.md.
set -eu

: "${HA_URL:?HA_URL environment variable is required}"
: "${HA_TOKEN:?HA_TOKEN environment variable is required}"

object_id="$1"
channel_id="$2"
entity_id="automation.${object_id}"

# `variables` makes channel_id available as a plain template variable in
# the automation's actions, e.g. `{{ channel_id }}` - NOT under
# `trigger.variables`, since automation.trigger doesn't populate `trigger`.
if ! http --check-status --ignore-stdin --timeout=300 --print=b \
  POST "${HA_URL%/}/api/services/automation/trigger" \
  "Authorization:Bearer ${HA_TOKEN}" \
  --raw "{\"entity_id\": \"${entity_id}\", \"variables\": {\"channel_id\": \"${channel_id}\"}}"; then
  echo "Home Assistant request failed for $entity_id" >&2
  exit 1
fi

echo "Triggered automation: $entity_id"
