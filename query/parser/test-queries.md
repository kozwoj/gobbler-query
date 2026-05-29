events(*)
events(last 7d)
events(last 24h)
events(datetime(2026-01-01 00:00:00.000)..datetime(2026-02-01 00:00:00.000))

events(*) | count
events(*) | take 100
events(last 7d) | sort by timestamp
events(last 7d) | sort by timestamp desc
events(last 7d) | sort by timestamp asc, level desc
events(last 7d) | project timestamp, message
events(last 7d) | project elapsed = endTime - startTime

events(last 7d) | where level == "Error"
events(last 7d) | where statusCode >= 400
events(last 7d) | where message contains "timeout"
events(last 7d) | where message startswith "ERR"
events(last 7d) | where name =~ "admin"
events(last 7d) | where isnull(userId)
events(last 7d) | where isnotnull(userId)
events(last 7d) | where isempty(message)
events(last 7d) | where level in ("Error", "Warning")
events(last 7d) | where code !in (200, 201, 204)
events(last 7d) | where durationMs between (100 .. 500)
events(last 7d) | where ts > ago(1h)

events(last 7d) | where level == "Error" and region == "eastus"
events(last 7d) | where level == "Error" or level == "Warning"
events(last 7d) | where not isnull(userId)
events(last 7d) | where (level == "Error" or level == "Critical") and region == "eastus"
events(last 7d) | where level == "Error" and region == "eastus" or level == "Warning"

events(last 7d) | summarize count()
events(last 7d) | summarize count() by region
events(last 7d) | summarize total = count() by region, level
events(last 7d) | summarize avg(durationMs) by region
events(last 7d) | summarize maxDur = max(durationMs), minDur = min(durationMs) by region
events(last 7d) | summarize dcount(userId) by service

events(last 7d) | where level == "Error" | project timestamp, message | take 50
events(last 7d) | where level == "Error" | sort by timestamp desc | take 10
events(last 7d) | where level == "Error" and region == "eastus" | summarize count() by service

events(last 7d) | join (users(*) | project userId, name) on userId
events(last 7d) | join (users(*)) on $left.userId == $right.id
events(last 7d) | join (orders(last 7d)) on userId, orderId

events                              → missing time window
events(last 7d) | bogus             → unknown stage keyword
events(last 7d) | take -1           → negative take count
events(last 7d) | where x + y       → scalar expression where bool expected
events(last 7d) | where (x + y) in ("a")  → non-field-ref left of 'in'