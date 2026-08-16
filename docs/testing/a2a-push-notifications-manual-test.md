# A2A Push Notifications — Manual Testing Guide

This guide walks through testing the A2A push notification implementation end-to-end.

## Prerequisites

1. Nanite running locally (default: http://localhost:8080)
2. A workflow or durable agent configured
3. A webhook receiver endpoint (options below)

## Option 1: Using webhook.site (easiest)

1. Go to https://webhook.site
2. Copy your unique URL (e.g., `https://webhook.site/a1b2c3d4-...`)
3. Use that URL in the `pushNotificationConfig` below

## Option 2: Using httpbin.org

Less reliable for testing (no visual interface), but works:
- URL: `https://httpbin.org/post`
- Check response body for echo of received data

## Option 3: Local webhook receiver

```bash
# Simple Python server to log webhooks
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class WebhookHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers['Content-Length'])
        body = self.rfile.read(content_length)
        print('\\n--- Webhook Received ---')
        print(f'Path: {self.path}')
        print(f'Headers: {dict(self.headers)}')
        print(f'Body: {body.decode()}')
        print('------------------------\\n')
        self.send_response(200)
        self.end_headers()

HTTPServer(('localhost', 9999), WebhookHandler).serve_forever()
"

# Then use URL: http://localhost:9999/webhook
```

## Test Case 1: Workflow-Backed Task with Push Notifications

### Submit Task

```bash
curl -X POST http://localhost:8080/api/a2a/jsonrpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "task-submit",
    "params": {
      "target": "researcher",
      "message": "Research the latest developments in quantum computing",
      "pushNotificationConfig": {
        "url": "YOUR_WEBHOOK_URL_HERE",
        "token": "test-bearer-token"
      }
    },
    "id": 1
  }'
```

### Expected Response

```json
{
  "jsonrpc": "2.0",
  "result": {
    "taskId": "01M...",
    "state": "working"
  },
  "id": 1
}
```

### Expected Webhook Notifications

You should receive **multiple** webhook POSTs as the task progresses:

**1. Transition to `working`** (immediately after submission):
```json
{
  "taskId": "01M...",
  "state": "working",
  "timestamp": "2026-08-15T...",
  "message": ""
}
```

**2. Transition to `completed`** (after workflow finishes):
```json
{
  "taskId": "01M...",
  "state": "completed",
  "timestamp": "2026-08-15T...",
  "message": ""
}
```

OR if it fails:
```json
{
  "taskId": "01M...",
  "state": "failed",
  "timestamp": "2026-08-15T...",
  "message": ""
}
```

### Verify Headers

Check that your webhook receiver got:
- `Content-Type: application/json`
- `Authorization: Bearer test-bearer-token` (if you provided a token)

## Test Case 2: Instance-Targeted Task

```bash
# First, get an instance ID from a durable agent
curl http://localhost:8080/api/durable-agents/instances | jq '.instances[0].id'

# Submit task to that instance
curl -X POST http://localhost:8080/api/a2a/jsonrpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "task-submit",
    "params": {
      "target": "msg://agent/nanite/INSTANCE_ID_HERE",
      "message": "Hello, can you help me?",
      "pushNotificationConfig": {
        "url": "YOUR_WEBHOOK_URL_HERE"
      }
    },
    "id": 2
  }'
```

Expected notifications: `working` → `completed` or `failed`

## Test Case 3: Retry Behavior (Simulated Failure)

To test the retry logic, you need a webhook endpoint that fails initially.

### Using a local Python server

```python
# retry_tester.py
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

attempt_count = 0

class RetryTestHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        global attempt_count
        attempt_count += 1
        
        content_length = int(self.headers['Content-Length'])
        body = self.rfile.read(content_length)
        
        print(f'\\nAttempt {attempt_count}')
        print(f'Body: {body.decode()}')
        
        # Fail first 2 attempts, succeed on 3rd
        if attempt_count < 3:
            print('Returning 500 (simulated failure)')
            self.send_response(500)
            self.send_header('Content-Type', 'text/plain')
            self.end_headers()
            self.wfile.write(b'Simulated failure')
        else:
            print('Returning 200 (success)')
            self.send_response(200)
            self.end_headers()

HTTPServer(('localhost', 9999), RetryTestHandler).serve_forever()
```

Run it:
```bash
python3 retry_tester.py
```

Submit task with `"url": "http://localhost:9999/webhook"`

Expected behavior:
1. First delivery attempt fails (HTTP 500) — immediate
2. Second attempt ~1 minute later (also fails)
3. Third attempt ~5 minutes later (succeeds)
4. Delivery record deleted

### Verify in Database

```bash
# Connect to Nanite's SQLite database
sqlite3 ~/.nanite/nanite.db

# Check pending deliveries
SELECT id, task_id, target_state, attempt_count, last_error, next_retry 
FROM a2a_push_deliveries;

# Initially you'll see:
# - attempt_count incrementing
# - last_error showing failure reason
# - next_retry showing future timestamp

# After max attempts or success, the row should be deleted
```

## Test Case 4: No Push Config (Control)

Submit a task without `pushNotificationConfig`:

```bash
curl -X POST http://localhost:8080/api/a2a/jsonrpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "task-submit",
    "params": {
      "target": "researcher",
      "message": "Research without webhooks"
    },
    "id": 3
  }'
```

Expected: Task works normally, but no deliveries are enqueued (check database).

## Monitoring Logs

Watch Nanite logs for push notification activity:

```bash
# In the Nanite terminal, look for:
# - "a2a push: enqueued delivery"
# - "a2a push: processing pending deliveries"
# - "a2a push: delivery successful"
# - "a2a push: delivery failed, scheduling retry"
# - "a2a push: max attempts reached"
```

Example log output:
```
INFO  a2a: task submit task_id=01M... target=researcher
DEBUG a2a push: enqueued delivery task_id=01M... state=working
INFO  a2a push: delivery successful task_id=01M... state=working
INFO  a2a: derived state differs from cached task_id=01M... cached=working derived=completed
DEBUG a2a push: enqueued delivery task_id=01M... state=completed
INFO  a2a push: delivery successful task_id=01M... state=completed
```

## Database Inspection

```sql
-- Check tasks with push config
SELECT id, target_ref, state, push_notification_config 
FROM a2a_tasks 
WHERE push_notification_config IS NOT NULL;

-- Check pending deliveries
SELECT * FROM a2a_push_deliveries;

-- Check delivery history (before deletion)
-- Note: Successful deliveries are deleted, so you'll only see:
-- - Currently retrying deliveries
-- - Failed deliveries that gave up
```

## Common Issues

### 1. No webhooks received

Check:
- Is background worker running? (Look for "a2a-push-delivery" in logs)
- Is task actually transitioning state? (Check `task-get`)
- Is webhook URL correct and reachable from Nanite server?
- Firewall blocking outbound HTTP?

### 2. Webhooks sent but webhook.site shows nothing

- webhook.site free tier has rate limits
- Try httpbin.org or local server instead

### 3. Authorization header missing

- Ensure you're setting either `token` (becomes `Bearer {token}`) or `auth` (used as-is)
- Check webhook receiver logs for actual headers received

### 4. Retries not happening

- Background worker runs every 30 seconds
- Check `next_retry` timestamp in database
- Manually update `next_retry` to `CURRENT_TIMESTAMP` to force immediate retry:

```sql
UPDATE a2a_push_deliveries 
SET next_retry = CURRENT_TIMESTAMP 
WHERE id = 'DELIVERY_ID';
```

## Success Criteria

✅ Webhook receiver shows notifications for each state transition  
✅ Database shows delivery records created and deleted  
✅ Logs show "delivery successful" messages  
✅ Failed deliveries retry with increasing backoff  
✅ After 3 failures, delivery is deleted (not infinitely retrying)  
✅ Tasks without push config still work normally  

## Cleanup

```sql
-- Clear all pending deliveries (if stuck in test)
DELETE FROM a2a_push_deliveries;

-- Clear test tasks
DELETE FROM a2a_tasks WHERE message LIKE '%test%';
```
