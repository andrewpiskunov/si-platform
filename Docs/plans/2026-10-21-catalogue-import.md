# Authorized catalogue import implementation plan

> Execute inline using superpowers:executing-plans; whole-stage fresh strongest review once, one Important/Critical correction pass. User authorized all local P0; no repeated requirements/approval.

**Goal:** FR20 resumable authorized local SIQ batch with durable deduplication and provenance.
**Spec:** Docs/26-CATALOGUE-IMPORT.md.
**Stack:** existing Go/chi/pgx/upload workers/os.Root; no new dependency or remote fetch.

## Task 1 — receipts, ingestion, library transaction

- [ ] RED tests in internal/ingest/catalogue_test.go for admin+write fresh auth, same-request concurrent retry, metadata conflict, hash verification, terminal failure replay; library tests for same receipt after pack purge, role loss, rights prefill.
- [ ] Migration20 catalogue_imports: id,owner_id FKaccount,namespace,external_id,request_hash,sha256,name,size,source_url,license,attribution,permission_basis,upload_id unique (noFK),pack_id defaultempty (noFK),created_at; uniqueowner/namespace/external_id. No rawarchive or secrets.
- [ ] internal/ingest CatalogueInput metadata validated bounded; CreateCatalogue(ctx,actor,in) returns Upload, CatalogID? Use Upload response existing ID/status/draft/error. Replays return existing upload or synthetic ready for retained pack receipt, synthetic failed expired if uploadpurged before success. Fresh auth/admin and hash checked before replay. Deleted/blocked target returnsnotfound/forbidden; never recreate. Extract create body to caller-owned transaction helper with post-commit filecleanup semantics; ordinaryCreate preserved.
- [ ] Hash check in processArchive before storing blobs. library.Create/createInAccess finds optional receipt by ImportID underaccountlock; adminrole check, existingpack receipt preventsresurrection; prefillRights confirmedfalse and setpack_id sameTX. Keep ImportID and existing dedup invariants.
- [ ] Privacy projection own metadata only; retention25/deletedowner30d/exportlease; tests and sqlc regen. Full affected realPG race.

## Task 2 — authenticated endpoint and CLI

- [ ] POST /api/catalogue-imports inside authoring routes. Existing identity mutation/CSRF safeguards, body≤16KiB strictJSON; admin role validated service-side. Ordinary owner upload endpoints provide resume/complete/status.
- [ ] internal/catalogueimport client + cmd/catalogue-import manifest path; SI_API_URL/SI_API_TOKEN env; HTTPS or explicit loopback HTTP; redirects refused. JSON≤1MiB,1..100 files; full preflight validate all metadata/IDs/duplicate keys and local size/hash before mutation; os.Root localpaths confined; no automatic remote retrieval. Rehash/recheck file before sending; serverhash authoritative. HTTP30s eachrequest, overallcontext1h; sequential4MiBchunks andbounded statuspoll, reportJSON allitems, nonsuccess exitsnonzero; skip terminalready; samekeyrequest resumes offset. No tokens in logs/errors/output. HTTPerror safe statuscode only.
- [ ] RED fakeHTTP/realHTTP tests invalidmanifest/path/hash/no sideeffects, deniedredirect/no tokenleak, repeat andoffsetresume, timeout, permanentfailure, readyexisting. Real server endpoint auth and endtoend ownSIQ.

## Task 3 — local pilot and acceptance

- [ ] CLI own fixture party→server worker→private drafts→repeat same IDs/count; source/rights preserved; public404; no external download. Tests isolated and disposable account cleanup. Docs26 usage, README, Docs11 honest localstatus/externalrightsnotverified.
- [ ] Full Go race/vet/build; webtests/check/build if affected; existing authoring/browser/privacy regression as appropriate. One fresh Astra high review exactstagebase..head; fixImportant/Critical once withRED→GREEN/fullsuite; FFmain; continueops/releasepolish.
