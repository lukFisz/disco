#!/bin/sh
# Harmless example script invoked by the "notify" command in config.yaml.example.
# Demonstrates how each param type lands in exec: integer/boolean/number
# substitute as their literal decimal/true-false form, user/channel/role as
# the picked entity's numeric Discord ID.
echo "message=$1 repeat=$2 urgent=$3 score=$4 target_user=$5 target_channel=$6 target_role=$7"
