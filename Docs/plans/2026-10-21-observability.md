# Observability implementation plan

> Implement inline with superpowers:executing-plans. One fresh strongest whole-stage review, one Important/Critical correction pass. Existing user fullP0 authorization applies.

**Goal:** bounded private operational metrics and usable scrape/alert instructions, without claiming unmeasured SLOs.
**Spec:** Docs/27-OBSERVABILITY.md.
**Architecture:** per-server Prometheus registry; instrumentation no DB work; bounded background aggregate reader; separate authenticated listener.
**Stack:** Go1.27.3, prometheus/client_golang1.25.0, chi/pgx existing.

## Task 1 — registry and secure endpoint

- [ ] RED tests internal/telemetry registry histogram/counter correctness, fixed categories under arbitraryinput; separateHandler bearer-only/noquery/noCORS/privateheaders, missingwrongtoken401, methods/path; max2scrapes andtimeout/cancel.
- [ ] New internal/telemetry owns registry and collectors. APIs New() *Metrics, ObserveHTTP(category,method,status,duration,in,out), ObserveRoom(kind,queue,duration,error), WSBytes andSlowClient counters; gauges setters or registeredfixed callbacks. Official Go/process collectors. Fixedcategoriesenum, no IDs/rawURL. Perregistry no globalregistration.
- [ ] Endpoint factory Handler(token string) (http.Handler,error) validates32..256 printabletoken; strictsingleAuthorization constanttimecompare, noquery, GETmetrics only. promhttp HandlerFor with MaxRequestsInFlight2/Timeout5s; optionswithouterrorlogsensitivevalues. No publicrouterroute.
- [ ] Test race and formatter validation; dependency pinned andtidied.

## Task 2 — HTTP/game and background PG instrumentation

- [ ] RED realHTTP/server tests counters includingfailures and unknownpathcardinality; real WS command metrics/result andqueue histogram; metrics absent publicendpoint. Existing WSupgrade and nativeZIP/streaming regression protectresponsewrapper.
- [ ] Server owns Metrics when Options.Metrics nil createone; middleware uses wrappedResponseWriter withUnwrap/hijack support andcountsreads/writes. Categoriesfixed account/catalogue/authoring/game/media/mcp/other; normalize method/status. Skip longWSduration, separatelycountattempts/currentconnectiongauge. Snapshotcallback captures server.mu only briefly; channel len safe; noDBunderlock.
- [ ] Room job contains submittedtime/kind; do delegatesdoKind defaultroom_work, wscommand usescommand. run observesqueue+processing onallpaths, no arbitrary commandlabels. Slowclient/WSapplicationpayloadbytes recorded actualenqueue/write withfixedmethods.
- [ ] background reader internal/telemetry or server/metrics collector: one goroutine perprocess,15s ticker/context2s; aggregates allsuccesspublishatomically; SQLfixedstatus allowlists, queues/knownAIspent/reserved andlogicalversionbytes; poolstatsnolocksDB. Lastsuccess/errorcounter, nozeroonfailure. Start/Close lifecycle explicit, tests realPG andpoolfailure/contextcancel.

## Task 3 — runtime wiring and operator artifacts

- [ ] cmd/server METRICS_ADDR/TOKEN parser tests, invalidconfigfailclosed, listenerbinderrorstartupfailure, shutdownserver5s; metricsfailuresdon'tcancelgameafterstartup. separate metrics listenerdefaultdisabled/no publicCaddyroute.
- [ ] .env.example, Dockerfile include catalogue CLI previouslyhostonly; composeopt-inprivate metrics config (no externalport/no secretscommitted); ops/prometheus.yml.example andalerts.yml, Docs27 thresholds/runbook; no monitoringmessages sent.
- [ ] Full realPG/media Go race/vet/build, applicable web/socket/browser checks. Actualauthenticatedmetrics scrape localwithownsecret notprinted; commandcounts/DBaggregateverified; one fresh Astrahighreview base..head thenTDDsinglefixpass/FFmain. Next stages load/recovery/releasepolish remainactive.
