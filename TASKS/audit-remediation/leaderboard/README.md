# Audit remediation raceboard

A dependency-free browser leaderboard for `findings.json`. It polls the source
every five seconds, ranks open findings by urgency, and provides filters plus a
detail view for evidence and recommendations.

## Run it

From the repository root:

```bash
python3 -m http.server 4173 --directory TASKS/audit-remediation
```

Then open <http://localhost:4173/leaderboard/>.

The page must be served over HTTP because browsers do not load JavaScript
modules and JSON reliably from `file://` URLs.

## Reuse it

Copy the `leaderboard/` directory beside another compatible `findings.json`, or
point it at a different source with a URL parameter:

```text
http://localhost:4173/leaderboard/?data=../another-findings.json
```

The minimum accepted shape is:

```json
{
  "findings": [
    {
      "id": "EXAMPLE-001",
      "severity": "high",
      "summary": "Example finding",
      "task_status": "not-started",
      "disposition": "remediate"
    }
  ]
}
```

Optional fields are normalized safely. The richer Nanite fields—categories,
package, confidence, evidence, recommendation, files, task file, revalidation
note, and architect-decision flag—appear when present.

## Verify the ranking logic

```bash
node --test TASKS/audit-remediation/leaderboard/leaderboard.test.mjs
```
