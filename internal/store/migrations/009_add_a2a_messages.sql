CREATE TABLE IF NOT EXISTS a2a_messages (
  id TEXT PRIMARY KEY,
  from_agent TEXT NOT NULL,
  to_agent TEXT NOT NULL,
  thread_id TEXT,
  reply_to TEXT REFERENCES a2a_messages(id),
  type TEXT NOT NULL DEFAULT 'message' CHECK(type IN ('message','help_request','directive','status_update','handoff')),
  subject TEXT,
  body TEXT NOT NULL,
  metadata TEXT DEFAULT '{}',
  priority INTEGER DEFAULT 2,
  status TEXT DEFAULT 'unread' CHECK(status IN ('unread','read','acknowledged','resolved')),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  read_at DATETIME,
  resolved_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_a2a_inbox ON a2a_messages(to_agent, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_a2a_thread ON a2a_messages(thread_id, created_at);
