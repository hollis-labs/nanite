Run the health-check skill: check status of all Tiamat services by curling their health endpoints:
- Hadron Daemon: http://127.0.0.1:8095/
- Volon GUI: http://127.0.0.1:8085/v1/tasks
- Cortex: http://127.0.0.1:8080/v1/health/readiness

For each service, measure response time. Present results as a formatted table showing Service Name, Status (UP/DOWN), HTTP Code, and Latency in ms. Use `curl -sf -o /dev/null -w "%{http_code}" <url> --max-time 3` for each check.
