Run the qhealth skill via a sub-agent. Launch an Agent tool (subagent_type: general-purpose, model: haiku) that checks health of portfolio services: Torque GUI (:8085/v1/tasks), Tesseract (:8089/v1/health/readiness; use the deployment's configured address if overridden), Hadron (:8095/v1/health). For each, measure HTTP status code and latency in ms using curl with 3s timeout. Also check Cerberus status via mcp__cerberus__cerberus_status. Return ONLY this compact format:

=== QHEALTH ===
Torque [UP/DOWN]  <code>  <latency>ms
Tesseract [UP/DOWN]  <code>  <latency>ms
Hadron    [UP/DOWN]  <code>  <latency>ms
Cerberus  [UP/DOWN]  <N> services managed, <M> running
================

Display the agent's response directly. No additional commentary.
