# Quota visibility implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL superpowers:executing-plans. Root Go/API/integration; independent web worker. One fresh whole-stage review, one correction pass, no re-review.

**Goal:** Видимые до загрузки действующие лимиты и остатки аккаунта.
**Architecture:** Read-only session-authenticated usage snapshot reuses exact StorageUsage under account lock; local UI panel refreshes without changing upload enforcement.
**Tech Stack:** Existing Go/pgx/chi/SvelteKit; no migrations/dependencies.
**Spec:** Docs/21-QUOTA-VISIBILITY.md.

## Global constraints

- 10GiB storage,200activepacks,100retainedthemes,500packversions/100themeversions,2activeownuploads. Existing serverenforcement unchanged.
- Limits250MiBSIQ/100MiBmedia/5MiBtheme; snapshotnotreservation, preparedbytes maygrow, errornotzero.
- Sessiononly, no userIDparameter, currentcredential/activeaccount verification; nootheruserscounts.
- Read-onlyGET doesnotexpireuploads; useexpires_at>clock_timestamp in activecount.

## Review focus

1. Deletedthemes remaincharged and count100; deletedpack mayleaveunattachedreadyassetcharge. DisplaymatchesexistingStorageUsage exactly.
2. Two tabs change usagewhilepanelopen: refreshactualsnapshot, nospacepromise/client-onlydecision.
3. Revoked/deletedaccount orlogout/differentlogin: nooldquota displayed, no leakedglobalcache.
4. Usagerequestfails after successfulupload: preserveoperationresult/form, labelstale/unknown, explicitretry.
5. Fileuploadresumes existingjob: noincorrectclientblock fromactiveown2; serverfinalauthority.

## Task1 — Usage API root

Files internal/library/usage.go+usage_test.go; internal/server/usage.go+usage_test.go; authoring.go routes.
`Service.Usage(ctx context.Context, actor accounts.Identity)(Usage,error)`, DTO nestedstorage/Counts packs/themes/uploads + limitsfieldsexactspec. begin(ctx,actor,"",true,"") then StorageUsage and countqueries underaccountlock. GET /api/usage existingidentity/writeJSON.
- [ ] RED ownusage/revisions/unattachedreadyuploads/retainedthemes/expiredactive, anonymous/token/revoked/deleted, otherusercounts excluded, remainingclamp.
- [ ] Implement DTO and shared10GiB/200/500 constants onlyif needed topreventdrift, notunrelatedrefactor. No changequotasemantics. RealDBboundaryfixtures bypassnormalcapacityonlyintests.
- [ ] HTTP RED endpoint401/403/no-store/exactJSONnumeric; implement route, focused/fullGo race DB/media/vet/build.

## Task2 — Before-upload UI independent worker

Files newweb/src/lib/{UsagePanel.svelte,usage-model.ts,usage-model.test.ts}; account/studio/editor, MediaUpload/ThemeUpload/ThemeLibrary asneeded; types.ts.
- [ ] RED formatting/projection/unknownvs0/lifecycle helper meaningfultests; sharedpanel limitlabels+refresh+staleerror. Independentcomponentstate, noaccountsharedcache.
- [ ] Mountaccount,studioSIQ,editorbeforemedia,themesbeforeZIP. Refresh afterownedmutation throughexplicitprop/callback or eventscopedtosamepage, no backgroundpolling. Editorcurrentrevision/selectedthemeversion visible.
- [ ] Staticlimits visiblebeforeGETresolved; dynamicerrornotzero; preserveforms/retry. Uploadcontrolskeep serverauthority/resume; nofalseestimatefromfilebytes.
- [ ] npm test/check/build; no runtime/Go/e2e/commits/review.

## Task3 — Acceptance root

Files e2e/usage.mjs/package.json, Docs11/21/README/ledger.
- [ ] RealChromium account/authoring/media/themes panelsbeforePOSTupload; countersupdate afterowncreate/version/upload; retainedtheme semantics viaAPItest; mocked503preserves usageandunsavedform, loginasother nooldcache, mobile/keyboard.
- [ ] Full relevantGo/web andallbrowser, onefreshwhole-stagereview, onefixpass REDGREEN; factualstatus/scopedcommit/FFmain. Continueprivacy/contentexport/retention.
