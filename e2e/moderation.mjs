import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { mkdtemp, readFile, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const base = process.env.BASE_URL || "http://localhost:5173";
const root = fileURLToPath(new URL("..", import.meta.url));
const output =
  process.env.ARTIFACT_DIR ||
  fileURLToPath(new URL("../.local/browser", import.meta.url));
const run = promisify(execFile);
assert.ok(
  process.env.DATABASE_URL,
  "Preview DATABASE_URL required for synthetic moderator",
);
const dir = await mkdtemp(join(tmpdir(), "si-moderation-"));
const browser = await chromium.launch();
const people = [],
  errors = [],
  suffix = randomBytes(6).toString("hex");
const password = randomBytes(20).toString("hex");
let checkpoint = "setup";
await mkdir(output, { recursive: true });
async function api(who, method, path, data) {
  const response = await who.context.request.fetch(base + "/api" + path, {
    method,
    data,
  });
  assert.ok(
    response.ok(),
    `${method} ${path}: ${response.status()} ${await response.text()}`,
  );
  return response.status() === 204 ? null : response.json();
}
async function person(role) {
  const context = await browser.newContext(),
    page = await context.newPage();
  const who = {
    context,
    page,
    username: `moderation_${role}_${suffix}`,
    registered: false,
  };
  people.push(who);
  page.on("pageerror", (error) => errors.push(error.stack || error.message));
  await api(who, "POST", "/account/register", {
    username: who.username,
    password,
    display_name: role,
  });
  who.registered = true;
  return who;
}
async function until(fn, message, timeout = 20000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw Error(`${checkpoint}: ${message}`);
}
async function fixture(name, args) {
  const path = join(dir, name);
  await run(
    process.env.MEDIA_FFMPEG || "ffmpeg",
    [
      "-v",
      "error",
      "-nostdin",
      "-y",
      ...args,
      "-threads",
      "1",
      "-filter_threads",
      "1",
      path,
    ],
    { timeout: 30000 },
  );
  return { name, data: await readFile(path) };
}
async function upload(who, draft, file, kind) {
  let job = await api(who, "POST", "/media-uploads", {
    name: file.name,
    size: file.data.length,
    kind,
  });
  const response = await who.context.request.patch(
    `${base}/api/uploads/${job.id}`,
    {
      data: file.data,
      headers: {
        "Content-Type": "application/octet-stream",
        "Upload-Offset": "0",
      },
    },
  );
  assert.equal(response.status(), 200);
  await api(who, "POST", `/uploads/${job.id}/complete`);
  await until(
    async () => {
      job = await api(who, "GET", `/uploads/${job.id}`);
      assert.notEqual(job.status, "failed", job.error);
      return job.status === "ready";
    },
    "media preparation",
    30000,
  );
  return api(who, "POST", `/drafts/${draft.card.id}/assets`, {
    upload_id: job.id,
    base_revision: draft.card.revision,
  });
}
try {
  const owner = await person("author"),
    mod = await person("staff"),
    reporter = await person("reader");
  await run(
    process.env.GO || "go",
    ["run", "./cmd/admin", "role", mod.username, "moderator"],
    { cwd: root, env: process.env, timeout: 60000 },
  );
  const picture = await fixture("picture.png", [
    "-f",
    "lavfi",
    "-i",
    "color=blue:size=96x64",
    "-frames:v",
    "1",
  ]);
  const sound = await fixture("sound.wav", [
    "-f",
    "lavfi",
    "-i",
    "sine=frequency=440:sample_rate=24000",
    "-t",
    "1",
    "-c:a",
    "pcm_s16le",
  ]);
  const movie = await fixture("movie.mp4", [
    "-f",
    "lavfi",
    "-i",
    "testsrc=size=96x64:rate=12",
    "-t",
    "1",
    "-pix_fmt",
    "yuv420p",
    "-c:v",
    "libx264",
    "-movflags",
    "+faststart",
  ]);
  const document = {
    id: "moderation-document",
    version: "5.0",
    name: `Проверка модерации ${suffix}`,
    language: "ru",
    info: { authors: ["Автор"] },
    rounds: [
      {
        id: "r1",
        name: "Раунд",
        type: "standard",
        info: {},
        themes: [
          {
            id: "t1",
            name: "Медиа",
            info: {},
            questions: [
              {
                id: "q1",
                type: "simple",
                price: 100,
                info: {},
                right: ["Ответ"],
                params: [
                  {
                    name: "question",
                    type: "content",
                    items: [{ type: "text", value: "Вопрос" }],
                  },
                ],
              },
              {
                id: "q2",
                type: "simple",
                price: 200,
                info: {},
                right: ["Другой ответ"],
                params: [
                  {
                    name: "question",
                    type: "content",
                    items: [{ type: "text", value: "Второй вопрос" }],
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  };
  let draft = await api(owner, "POST", "/drafts", { document });
  for (const [file, kind] of [
    [picture, "image"],
    [sound, "audio"],
    [movie, "video"],
  ])
    draft = await upload(owner, draft, file, kind);
  const content = (kind, file) => ({
    type: kind,
    value: draft.assets
      .find((a) => a.name.endsWith("-" + file.name))
      .name.split("/")
      .slice(1)
      .join("/"),
    is_ref: true,
    wait_for_finish: true,
  });
  const question = document.rounds[0].themes[0].questions[0];
  question.params[0].items.push(
    content("image", picture),
    content("audio", sound),
  );
  question.params.push({
    name: "answer",
    type: "content",
    items: [
      { type: "text", value: "Пояснение автора" },
      content("video", movie),
    ],
  });
  draft = await api(owner, "PUT", `/drafts/${draft.card.id}`, {
    base_revision: draft.card.revision,
    document,
  });
  const id = draft.card.id,
    revision = draft.card.revision;
  const rights = {
    license: "Own",
    source_url: "original",
    attribution: "Автор",
    confirmed: true,
  };
  const submit = () =>
    api(owner, "POST", `/drafts/${id}/publish`, { revision, rights });
  await submit();
  checkpoint = "pinned media review";
  await mod.page.goto(base + "/moderation");
  const card = mod.page.locator(`[data-pack-id="${id}"]`);
  await card
    .getByRole("button", { name: "Просмотреть вопросы", exact: true })
    .click();
  const preview = mod.page.getByTestId("moderation-preview");
  await preview
    .getByRole("heading", {
      name: `${document.name} · версия ${revision}`,
      exact: true,
    })
    .waitFor();
  const mediaRequests = [];
  mod.page.on("request", (request) => {
    if (request.url().includes("/prepared/"))
      mediaRequests.push(new URL(request.url()));
  });
  await preview
    .getByRole("button", { name: "Показать медиа вопроса · 2", exact: true })
    .click();
  await until(
    () =>
      preview
        .locator("img")
        .evaluateAll(
          (nodes) =>
            nodes.length === 1 &&
            nodes[0].complete &&
            nodes[0].naturalWidth > 0,
        ),
    "image preview",
  );
  await preview.getByLabel(/^Качество предпросмотра/).selectOption("economy");
  await until(
    () =>
      preview
        .locator("img")
        .evaluateAll(
          (nodes) =>
            nodes.length === 1 &&
            nodes[0].complete &&
            nodes[0].naturalWidth > 0,
        ),
    "economy image",
  );
  await preview
    .getByRole("button", { name: "Показать медиа вопроса · 3", exact: true })
    .click();
  await until(
    () =>
      preview
        .locator("audio")
        .evaluateAll(
          (nodes) =>
            nodes.length === 1 && nodes[0].readyState >= 1 && nodes[0].paused,
        ),
    "audio metadata without autoplay",
  );
  await preview
    .getByRole("button", { name: "Показать медиа ответа · 2", exact: true })
    .click();
  await until(
    () =>
      preview
        .locator("video")
        .evaluateAll(
          (nodes) =>
            nodes.length === 1 && nodes[0].readyState >= 1 && nodes[0].paused,
        ),
    "answer video without autoplay",
  );
  assert.ok(mediaRequests.length >= 3);
  assert.ok(
    mediaRequests.every(
      (url) => url.searchParams.get("revision") === String(revision),
    ),
    "preview revision unpinned",
  );
  await preview.getByLabel(/^Вопрос для проверки/).selectOption("q2");
  assert.equal(
    await preview.locator("img,audio,video").count(),
    0,
    "old media survived question change",
  );
  checkpoint = "author rejection feedback";
  await card
    .getByLabel("Причина решения", { exact: true })
    .fill("Добавьте пояснение источника");
  await card
    .getByRole("button", { name: "Вернуть автору", exact: true })
    .click();
  await until(
    async () =>
      (await api(owner, "GET", `/drafts/${id}`)).card.status === "rejected",
    "rejection durable",
  );
  await owner.page.goto(base + `/studio/${id}`);
  await owner.page
    .getByTestId("moderation-history")
    .getByText("Добавьте пояснение источника", { exact: true })
    .waitFor();
  await submit();
  await mod.page
    .getByRole("button", { name: "Обновить очередь", exact: true })
    .click();
  await card.getByRole("button", { name: "Опубликовать", exact: true }).click();
  await until(
    async () =>
      (await api(owner, "GET", `/drafts/${id}`)).card.status === "published",
    "approval durable",
  );
  const report1 = await api(reporter, "POST", `/catalogue/${id}/reports`, {
    revision,
    reason: "Проверить атрибуцию",
  });
  const report2 = await api(owner, "POST", `/catalogue/${id}/reports`, {
    revision,
    reason: "Проверить пояснение",
  });
  const report3 = await api(mod, "POST", `/catalogue/${id}/reports`, {
    revision,
    reason: "Проверить старую версию",
  });
  await mod.page
    .getByRole("button", { name: "Обновить жалобы", exact: true })
    .click();
  const reportCard = (id) => mod.page.locator(`[data-report-id="${id}"]`);
  checkpoint = "dismiss and resolve";
  await reportCard(report1.id)
    .getByLabel("Причина решения по жалобе", { exact: true })
    .fill("Атрибуция присутствует");
  await reportCard(report1.id)
    .getByRole("button", { name: "Отклонить жалобу", exact: true })
    .click();
  await reportCard(report1.id).waitFor({ state: "detached" });
  await reportCard(report2.id)
    .getByLabel("Причина решения по жалобе", { exact: true })
    .fill("Пояснение проверено");
  await reportCard(report2.id)
    .getByRole("button", { name: "Завершить рассмотрение", exact: true })
    .click();
  await reportCard(report2.id).waitFor({ state: "detached" });
  checkpoint = "historic report block";
  document.name += " — новая";
  draft = await api(owner, "PUT", `/drafts/${id}`, {
    base_revision: revision,
    document,
  });
  await api(owner, "POST", `/drafts/${id}/publish`, {
    revision: draft.card.revision,
    rights,
  });
  await api(mod, "POST", `/moderation/${id}`, {
    revision: draft.card.revision,
    decision: "approve",
    reason: "",
  });
  await reportCard(report3.id)
    .getByRole("button", {
      name: `Просмотреть версию ${revision}`,
      exact: true,
    })
    .click();
  await preview
    .getByRole("heading", {
      name: `Проверка модерации ${suffix} · версия ${revision}`,
      exact: true,
    })
    .waitFor();
  checkpoint = "stale preview response";
  const historic = await api(mod, "GET", `/drafts/${id}?revision=${revision}`);
  draft = await api(owner, "PUT", `/drafts/${id}`, {
    base_revision: draft.card.revision,
    document,
  });
  await api(owner, "POST", `/drafts/${id}/publish`, {
    revision: draft.card.revision,
    rights,
  });
  await mod.page
    .getByRole("button", { name: "Обновить очередь", exact: true })
    .click();
  let held, requested;
  const heldRequest = new Promise((resolve) => (requested = resolve));
  const oldURL = `**/api/drafts/${id}?revision=${revision}`;
  await mod.page.route(oldURL, (route) => {
    held = route;
    requested();
  });
  await reportCard(report3.id)
    .getByRole("button", {
      name: `Просмотреть версию ${revision}`,
      exact: true,
    })
    .click();
  await heldRequest;
  await card
    .getByRole("button", { name: "Просмотреть вопросы", exact: true })
    .click();
  await preview
    .getByRole("heading", {
      name: `${document.name} · версия ${draft.card.revision}`,
      exact: true,
    })
    .waitFor();
  const oldFinished = mod.page.waitForEvent("requestfinished", {
    predicate: (request) =>
      request.url() === base + `/api/drafts/${id}?revision=${revision}`,
  });
  await held.fulfill({ status: 200, json: historic });
  await oldFinished;
  await mod.page.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
  assert.equal(
    await preview
      .getByRole("heading", {
        name: `${document.name} · версия ${draft.card.revision}`,
        exact: true,
      })
      .count(),
    1,
    "late historic response replaced newer preview",
  );
  await mod.page.unroute(oldURL);
  await reportCard(report3.id)
    .getByLabel("Причина решения по жалобе", { exact: true })
    .fill("Обнаружено нарушение в архивной версии");
  await reportCard(report3.id)
    .getByRole("button", { name: "Заблокировать пакет", exact: true })
    .click();
  await reportCard(report3.id).waitFor({ state: "detached" });
  await mod.page.getByLabel(/^Статус жалоб/).selectOption("resolved");
  await reportCard(report1.id)
    .getByText("Атрибуция присутствует", { exact: true })
    .waitFor();
  await reportCard(report2.id)
    .getByText("Пояснение проверено", { exact: true })
    .waitFor();
  await reportCard(report3.id)
    .getByText("Обнаружено нарушение в архивной версии", { exact: true })
    .waitFor();
  checkpoint = "local failure recovery";
  await mod.page.route("**/api/reports?**", (route) =>
    route.fulfill({ status: 503, json: { error: "unavailable" } }),
  );
  await mod.page
    .getByRole("button", { name: "Обновить жалобы", exact: true })
    .click();
  await mod.page.getByRole("alert").first().waitFor();
  assert.equal(
    await reportCard(report1.id).count(),
    1,
    "refresh failure discarded loaded list",
  );
  await mod.page.unroute("**/api/reports?**");
  await mod.page
    .getByRole("button", { name: "Обновить жалобы", exact: true })
    .click();
  await until(
    async () => (await mod.page.getByRole("alert").count()) === 0,
    "error cleared on retry",
  );
  checkpoint = "report and history pagination";
  const resolved = (
    await api(mod, "GET", "/reports?status=resolved")
  ).items.filter((item) => item.pack_id === id);
  assert.equal(resolved.length, 3);
  const firstPage = resolved.slice(0, 2),
    lastPage = resolved.slice(2);
  let cursorUsed = false;
  await mod.page.route("**/api/reports?**", (route) => {
    const after = new URL(route.request().url()).searchParams.get("after");
    cursorUsed ||= after === firstPage[1].id;
    return route.fulfill({
      status: 200,
      json: after
        ? { items: lastPage }
        : { items: firstPage, next_after: firstPage[1].id },
    });
  });
  await mod.page
    .getByRole("button", { name: "Обновить жалобы", exact: true })
    .click();
  await mod.page
    .getByRole("button", { name: "Следующие жалобы (до 50)", exact: true })
    .click();
  await reportCard(lastPage[0].id).waitFor();
  assert.equal(cursorUsed, true);
  assert.equal(await mod.page.locator("[data-report-id]").count(), 3);
  await mod.page.unroute("**/api/reports?**");
  const history = (await api(owner, "GET", `/drafts/${id}/decisions`)).items;
  assert.ok(history.length >= 3);
  let historyCursor = false;
  await owner.page.route(`**/api/drafts/${id}/decisions?**`, (route) => {
    const after = new URL(route.request().url()).searchParams.get("after_id");
    historyCursor ||= after === String(history[0].id);
    return route.fulfill({
      status: 200,
      json:
        after === "0"
          ? { items: history.slice(0, 1), next_after_id: history[0].id }
          : { items: history.slice(1) },
    });
  });
  const historyPanel = owner.page.getByTestId("moderation-history");
  await historyPanel
    .getByRole("button", { name: "Обновить историю решений", exact: true })
    .click();
  await historyPanel
    .getByRole("button", { name: "Следующие решения (до 50)", exact: true })
    .click();
  await historyPanel
    .getByText("Обнаружено нарушение в архивной версии", { exact: true })
    .waitFor();
  assert.equal(historyCursor, true);
  await owner.page.unroute(`**/api/drafts/${id}/decisions?**`);
  for (const width of [390, 820, 1440]) {
    await mod.page.setViewportSize({ width, height: 900 });
    assert.ok(
      await mod.page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "moderation horizontal overflow",
    );
    await mod.page.screenshot({
      path: output + `/moderation-${width}.png`,
      fullPage: true,
    });
  }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      pinned_media: true,
      no_autoplay: true,
      author_reason: true,
      dismiss: true,
      resolve: true,
      historic_block: true,
      terminal_audit_preserved: true,
      local_refresh_retry: true,
      stale_preview_guard: true,
      pagination: true,
      mobile: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error({ checkpoint, errors });
  throw error;
} finally {
  for (const who of people) {
    if (who.registered) {
      const response = await who.context.request.delete(
        base + "/api/account/me",
        { data: { password } },
      );
      assert.ok(response.ok(), "synthetic account cleanup");
    }
    await who.context.close();
  }
  await browser.close();
  await rm(dir, { recursive: true, force: true });
}
