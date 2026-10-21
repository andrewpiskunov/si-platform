# Пользовательские темы — план реализации

> **For agentic workers:** использовать superpowers:executing-plans для ledger/приёмки и superpowers:dispatching-parallel-agents для независимых ограниченных модулей. Один whole-stage review после интеграции, без цепочки reviewer на каждую задачу. Шаги отмечаются по фактической проверке.

**Goal:** загружать, проверять и применять приватные версии оформления ко всей игровой сессии.
**Architecture:** типизированный ZIP → существующая очередь ingest/FFmpeg → immutable theme_versions; атомарная ссылка в game.State и room_theme_pins, приватный HTTP и layout-provider. Игровая готовность независима от декора.
**Tech Stack:** текущие Go/chi/pgx/PostgreSQL/blob, FFmpeg sandbox, SvelteKit/TypeScript; без новых внешних сервисов.
**Spec:** [Docs/16-CUSTOM-THEMES.md](../16-CUSTOM-THEMES.md), продуктовые требования [Docs/08](../08-THEMES.md).

## Global Constraints

- Архив 5 MiB, распаковка 20 MiB/50 записей, JSON 64 KiB, до 3 уникальных изображений, изображение 2 MiB/4 Мп.
- PNG/JPEG/WebP → проверенный PNG; все standard ≤2 MiB, все economy ≤1 MiB; общий job≤5 минут, 2 workers.
- Поля/палитра/контраст/размеры — в точности Docs16; никакого пользовательского CSS/JS/HTML/SVG/внешних ресурсов.
- 100 тем/100 версий, общая квота 10 GiB; live session, optimistic revision, идемпотентность; source+derivatives зарезервированы до Put.
- До 12 игроков и 20 зрителей, безопасная область кнопки; менять тему в lobby/paused. Ordinary delete сохраняет pin; block/account delete отзывает.
- Русский UI, пример ZIP и инструкция; местная контрастная тема, экономный режим, reduced-motion.

## Review Focus

- В ZIP дубликаты/псевдонимы/ошибки CRC/объявленные размеры: не читать без лимита, не принимать чужой путь или активный контент (Task1).
- Сохранение готового результата, отзыв сессии, параллельное применение/блокировка: права и pin проверяются под транзакционными блокировками, повтор безопасен (Task2/3).
- Удаление из библиотеки, удаление аккаунта и редактирование после pin имеют разные последствия; GC не удаляет используемые версии (Task2/3).
- Поздний ответ HTTP старой темы/смена комнаты/каталог/несколько вкладок не применяют чужое оформление; ошибки декора не тормозят buzzer (Task4/5).
- Смена только качества/локального контраста и reconnect не скачивают лишний набор или меняют оформление других участников; бюджеты и читабельность на 12 карточках (Task4/5).

## Task 1 — Проверяемый формат и подготовка (`internal/themes/model.go`, `archive.go`, `prepare.go`, tests)

**Produces:**
- `Palette{Background,Surface,SurfaceAlt,Border,Text,Muted,Accent,AccentText,Danger,Success,Focus string}` JSON snake_case; `Shape{Radius,BorderWidth int}`.
- `Manifest{SchemaVersion int;Name,Author,Provenance,Font string;Palette Palette;Cards,Buttons Shape;Background,Logo,Ornament string}` JSON snake_case. `Issue{Path,Code,Message string}`; `ValidationError{Issues []Issue}`; `Issues(error) []Issue` exposes safe diagnostics; `ErrInvalid` etc defined inmodel.
- `Asset{Name string;Prepared media.Prepared}`; `Prepared{Manifest Manifest;Assets []Asset;Hash,OriginalKey string;OriginalSize,SizeBytes int64}` JSON snake_case, Validate checks fullytrustedprofile+hash+logicalsize. Hash = SHA256 of canonical JSON `{manifest,assets}` in sorted asset name order, excludes archive hash and size. Logicalsize = archivebytes + standard/economy bytes of each unique asset.
- `Read(ctx,io.ReaderAt,size int64)(*Archive,error)` validates archive/manifest, computes archiveSHA256. Archive owns no filesystem; bounded ZIP entries and original reader live until caller closes it.
- `Prepare(ctx,*media.Processor,*Archive)(*Result,error)`; `Result{Prepared Prepared;Files []media.Output}` and `Close() error` cleans only its privateprocessorresults. Nilprocessor allows palette-only theme, rejects images with diagnostic. No blob/PG writes here.

- [x] RED tests: validpalette-only and 3 images; unknown/duplicateJSON, invalidcolor/contrast/font/radius; normalizedduplicatepaths, symlink/traversal/ZIPbomb/CRC, unknownfiles and limits.
- [x] Implement strict typed parsing and exactbudget checks; unused images rejected, same file referenced twice countedonce. Validate provenance as plainboundedtext, never fetchURLs.
- [x] RED actualsandbox PNG/JPEG/WebP, animation/malformed/4MP/budget; preserve sourcehash, validPreparedhash/size, cancellation and cleanup.
- [x] GREEN `MEDIA_INTEGRATION=1 go test -race ./internal/themes` (parser tests) and vet; exactmodels available beforedependent tasks. Scopedcommit afterintegration with Task2.

## Task 2 — Версии, queue, quota (`internal/themes/service.go`, `internal/ingest/theme.go`, tests; migration00008; library usage/GC)

**Consumes:** Task1 Prepared/Result. **Produces:**
- `themes.New(*pgxpool.Pool)*Service`; `Card{ID,OwnerID,Name,Author string;Revision int;SizeBytes int64;Blocked bool}`, `Version{Card Card;Prepared Prepared}`; JSON card/prepared.
- `List(ctx,accounts.Identity)([]Card,error)`, `Get(ctx,actor,id,revision)(Version,error)`, `Attach(ctx,actor,id,baseRevision,uploadID)(Version,error)` (idempty→new), `Delete(ctx,actor,id) error`, `Block(ctx,actor,id,blocked bool,reason string) error`, `Pinned(ctx,id,revision,hash)(Version,error)`; errors Unauthorized/Forbidden/NotFound/Conflict/Invalid/Quota/Blocked/Unavailable. Sessiononly forwrites andownerreads; moderator/admin forBlock.
- `ingest.CreateTheme(ctx,actor,name,size)(Upload,error)`, Upload.Theme `*themes.Prepared`, Upload.Issues `[]themes.Issue`; purpose `theme`, result_theme and diagnostic_issues. Existing chunk/complete/poll reused. Asset and Theme mutuallyexclusive.
- Migration00008: `themes(id,owner_id,current_revision,blocked,deleted_at,created_at,updated_at)`, `theme_versions(theme_id,revision,prepared,size_bytes,created_at)` compositePK/FK; `theme_receipts(upload_id PRIMARY KEY,theme_id,revision)`; `theme_moderation(id bigserial,theme_id,actor_id,blocked,reason,created_at)`; `room_theme_pins(room_id PRIMARY KEY,theme_id,revision,hash)` FKversion. Extenduploads constraints/result fields. Themeversions size includesarchive+bothqualities. Every retainedthemeversion charged, plusreadyunattachedthemeuploads; firstattach transferscharge exactlyonce.

- [x] RED realPG tests session/API-scope denial, own/foreign upload, baseconflict andduplicatecreate/update, 100/100limits, quota sharedwithpacks andreadyuploads, revoked/blocked/deletedowner.
- [x] Implementservice with account lock and livecredential check, immutable revisions, exact trustedresult fromreadyupload. Receipt checkedbeforebaserevision; replay returns originally attachedversion, mismatchedtargetconflicts. Delete hideslibrary/newapply butPinned ignores ordinarydelete. Blockaudit andlivePinned revocation.
- [x] RED durableingest realarchive: create/upload/ready/attach; 5mincontext, restart/epochloss/authrevocation/cancel; reservations includearchive+alloutputs beforePut; GC respects theme_versions andreadyuploads; source bytes preserved.
- [x] Updateusage underaccountlock and workerfinish; validation errors keptsafe/bounded. `drafts:*` API token cannotcreate/read themeuploads orgetthemes. ExistingSIQ/media unchanged.
- [x] GREEN realPG/actualsandbox race themes/ingest/library; rootappliesmigration topreviewonlyaftertests. Commit formats/data together.

## Task 3 — Pin, изменение и HTTP (root; game/postgres/server/cmd)

**Produces:**
- `game.Appearance{Builtin,ThemeID string;Revision int;Hash string}` JSON builtin/theme_id/revision/hash omitempty; Validate accepts ONLYbuiltinclassic/pirate/anime OR theme32hex+positive revision+hash64hex. State/View.Appearance optionalpointer; oldsnapshots fallbackGamePin.Theme. `AppearancePayload{Appearance Appearance}` andv2 `set_appearance` host/lobby-or-paused. ExpectedSeq required. No effect on score/stage/readiness/deadlines.
- `postgres.GameSetup.Appearance *game.Appearance`, `GamePin.Appearance *game.Appearance`; roomcreation authorizescustomversion under account/theme locks andwritesroom_theme_pins withsnapshot. ExistingTheme stringkeptbackwardscompatible.
- `ExecuteAppearance(ctx,roomID,participantID,game.Command,AppearanceActor)(Outcome,error)` shares existingExecute transaction/hash/receipt; `AppearanceActor{UserID,CredentialID string}` keepsstore independentofaccountservice; normalExecute rejects set_appearance. Customliveaccountsession/ownversion checkedintransaction; builtin needs onlyhostroomtoken. Pin andsnapshot savedtogether. Neverencodeaccountcredentialineventpayload/hash.
- HTTP `POST /theme-uploads{name,size}`; `GET /themes`→{themes:Card[]}; `POST /themes{upload_id}` / `POST /themes/{id}/versions{upload_id,base_revision}`→Version; `GET/DELETE /themes/{id}?revision=N`; ownerasset `GET /themes/{id}/assets/{index}?revision=N&quality=standard|economy`, owneroriginal `GET /themes/{id}/archive?revision=N`; `POST /themes/{id}/block{blocked,reason}`.
- Room `POST /rooms/{id}/appearance{appearance,command_id,expected_seq}` authenticatedBearerroomtoken andcookieowner forcustom. Use explicitcookie identity path; roomBearer mustnotshadowaccountcookie. Room GETappearance→`{appearance,status:'ready'|'unavailable',manifest?,assets:[{index,name,width,height,standard_size,economy_size}]}`; assetGET `/rooms/{id}/appearance/assets/{index}?hash=...&quality=...` Bearerroom andlivepinvalidation. Ownerpreviewasset usesaccountsession. OriginalZIP neverroomaccessible.
- Bounded5sserverchecker foractive roomIDs queriesroom_theme_pins/themes/users inbatches, detectsavailabilitychange, sends `appearance_refresh` throughroomqueue. ReadAPI remainsauthoritative/rechecksACL. No per-clientpolling. IncludeblockCLI/HTTPaudit.

- [x] RED core/PG: onlyhost lobby/paused; invalidref/payload/seq; duplicate; snapshotreload; pin survivesnewversion/ordinarydelete, blocked/deletedaccountandrevokedcredential cannotapply/deliver. Commandidentitydoesnotleakcredentials.
- [x] Implementshared Execute branch withtransactional auth/pin. Applyaccountlockbeforetheme/room locks consistently. DenyWSset_appearance evenifpayloadvalid; HTTPdoesvalidation androomactorordering.
- [x] RED HTTP realPG: ownerpreview/original, role/foreignroom/futureversion denials, currentpinchangedandoldURLrevoked, fallbackmanifest onblock, refreshonblock/unblock/accountdelete. Slowblobdata doesnotoccupyroomactor.
- [x] Integrateserver/cmd, bounded JSONandblobserving; addunitcheckertrigger test andrealWSrevocation. GREEN core/postgres/server race+vet; noadvancegamefromthemechange. Commit.

## Task 4 — Библиотека и оболочка (`web/**`, после стабильных DTO)

Add `ThemeLibrary`, `ThemeUpload`, `ThemePreview`, `ThemeChooser`, `ThemePreferences`, `appearance.ts/tests`; extendtypes/API/room-theme/layout/room/CreateRoom/studio navigation/app.css. Builtinart `web/static/themes/{pirate,anime}.svg` authoredhere, exampleZIPdownload servedfromstaticwithreadablemanifest.

- [x] RED modeltests exactallowlistedCSS mapping (neverrawCSS), roomcontextlinks/tabseparation, staleasyncresponse cancellation, releaseBlobURLs; localclassic/economy precedence andbudgets.
- [x] Implementprivate `/themes` UI resumablethemeupload→issues→attach/newversion; showmobile/desktop, qualities, sizes, author/provenance; delete andcurrentversion. ZIPtemplate and Docs guide linked.
- [x] Createlobby acceptsappearance; connectedhost canchangeonlylobby/paused, pausedUIwarns andexplicitapply. Readonlyparticipantpreferences independentofroomstate.
- [x] Layoutprovider resolves roomroute or explicit?room, parses pertabcredentials; themeGET/assetfetchsmallblob withabortand5stimeout. Applycurrentthemeonlyaftervalidload; error/block→classicnotice; reloadonstateappearance/appearance_refresh/reconnect/visibility. No blockinggamereadiness. No duplicateassetfetchforunrelatedsnapshot.
- [x] Surfacepalette opaque; decorativeassets pointer-eventsnone andbounded; focusandkeyboardvisible; CSS vars radius/border/font allowlisted. Ownbuiltinmap/animeillustrations, reducedmotion, noexternalfonts/resources. Defaultnavigationnoinheritedtheme.
- [x] GREEN npmtest/check/build; ownerpreview/newversionconflicts preserveuploadedresult; commitafterbrowserintegration.

## Task 5 — Браузер, пример и обзор (root)

- [x] Add `examples/themes/` JSON/README and deterministic ZIP generator; valid small ownimages sample. Unitvalidateexample. Downloadsameexample fromweb/static, no secrets/thirdpartyassets.
- [x] RED thenGREEN `e2e/themes.mjs`: ownerimport/validationerror/preview; 12players customtheme; unchangedbuzzerwithslow/brokenassets; pause/switch/reconnect; privateoriginal; pin afternewversion/delete; block→fallback viaWS; cataloguecontext/independenttab; localclassic/economy; mobile12cards/focus/reducedmotion; no unexpectedHTTPexternalrequests orpageerrors.
- [x] RealPG+actualmedia `go test -race ./...`, vet/build, webcheck/test/build, native12, fullChromiumsuite; recordactualbundle/themebytes andlimitations. Runtimecontainer verificationremainsseparateifDockerabsent.
- [ ] Onefreshwhole-stage review base da16219; regradeactualeffects; onecorrectionpass RED→GREEN plusrelevantsuites. Docs08/11/16/README updatedwithactualstatus.
- [ ] Scopedcommits +fastforwardlocalmain; continueAI/MCP,privacy/catalogueandrelease. WholeprojectnotcompleteuntilremainingP0delivered.
