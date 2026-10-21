import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
const base = process.env.BASE_URL || "http://localhost:5173";
const output =
  process.env.ARTIFACT_DIR ||
  fileURLToPath(new URL("../.local/browser", import.meta.url));
const directory = await mkdtemp(join(tmpdir(), "si-quota-"));
const browser = await chromium.launch(),
  people = [],
  errors = [];
const suffix = randomBytes(6).toString("hex"),
  password = randomBytes(20).toString("hex");
let checkpoint = "setup";
await mkdir(output, { recursive: true });
async function api(who, method, path, data) {
  const deadline = Date.now() + 60000;
  for (;;) {
    const r = await who.context.request.fetch(base + "/api" + path, {
      method,
      data,
    });
    if (
      r.status() === 429 &&
      path.startsWith("/account/") &&
      Date.now() < deadline
    ) {
      const seconds = Number(r.headers()["retry-after"]);
      assert.ok(
        Number.isFinite(seconds) && seconds > 0 && seconds <= 10,
        "invalid Retry-After",
      );
      await new Promise((resolve) => setTimeout(resolve, seconds * 1000));
      continue;
    }
    assert.ok(r.ok(), `${method} ${path}: ${r.status()} ${await r.text()}`);
    return r.status() === 204 ? null : r.json();
  }
}
async function person(name) {
  const context = await browser.newContext(),
    page = await context.newPage(),
    who = {
      context,
      page,
      username: `quota_${name}_${suffix}`,
      registered: false,
    };
  people.push(who);
  page.on("pageerror", (e) => errors.push(e.stack || e.message));
  await api(who, "POST", "/account/register", {
    username: who.username,
    password,
    display_name: name,
  });
  who.registered = true;
  return who;
}
async function until(fn, message) {
  const end = Date.now() + 25000;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw Error(checkpoint + ": " + message);
}
async function quota(page, field, pattern) {
  const locator = page.getByTestId("quota-" + field);
  await until(
    async () => pattern.test(await locator.innerText()),
    `quota ${field} ${pattern}`,
  );
  return locator;
}
try {
  const owner = await person("author"),
    other = await person("other"),
    page = owner.page;
  await page.goto(base + "/studio");
  await quota(page, "packs", /0 из 200/);
  assert.match(await page.getByTestId("quota-usage").innerText(), /250 МиБ/);
  const file = page.getByLabel("Файл SIQ", { exact: true });
  assert.ok(
    await page
      .getByTestId("quota-usage")
      .evaluate(
        (panel) =>
          panel.compareDocumentPosition(
            document.querySelector("input[type=file]"),
          ) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    "SIQ quota must precede file input",
  );
  checkpoint = "create and revise";
  await page
    .getByRole("button", { name: "＋ Новый пакет", exact: true })
    .click();
  await page.waitForURL(/\/studio\/[a-f0-9]{32}$/);
  const id = new URL(page.url()).pathname.split("/").pop();
  await quota(page, "packs", /1 из 200/);
  await page.getByLabel("Название", { exact: true }).fill("Квота " + suffix);
  await until(async () => {
    const d = await api(owner, "GET", `/drafts/${id}`);
    return d.card.name === "Квота " + suffix && d.card.revision > 1;
  }, "autosave");
  await until(
    async () =>
      /Версии пакета: 2 из 500/.test(
        (await page.getByTestId("quota-usage").innerText()).replace(
          /\s+/g,
          " ",
        ),
      ),
    "pack revision count",
  );
  checkpoint = "media mutation refresh";
  const png = join(directory, "quota.png");
  await promisify(execFile)(
    process.env.MEDIA_FFMPEG || "ffmpeg",
    [
      "-v",
      "error",
      "-nostdin",
      "-y",
      "-f",
      "lavfi",
      "-i",
      "color=red:size=96x64",
      "-frames:v",
      "1",
      "-threads",
      "1",
      png,
    ],
    { timeout: 30000 },
  );
  let uploadSeen = false;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().endsWith("/api/media-uploads"))
      uploadSeen = true;
  });
  const beforeMedia = (await api(owner, "GET", "/usage")).storage.used_bytes;
  await page.getByLabel("Медиафайл", { exact: true }).setInputFiles(png);
  assert.equal(uploadSeen, false);
  assert.ok(
    await page
      .getByTestId("quota-usage")
      .evaluate(
        (panel) =>
          panel.compareDocumentPosition(
            document.querySelector("input[accept]"),
          ) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    "media quota must precede file input",
  );
  await page
    .getByRole("button", { name: "Загрузить и прикрепить", exact: true })
    .click();
  await until(
    async () => (await api(owner, "GET", `/drafts/${id}`)).assets.length === 1,
    "media attached",
  );
  assert.equal(uploadSeen, true);
  const afterMedia = (await api(owner, "GET", "/usage")).storage.used_bytes;
  assert.ok(afterMedia > beforeMedia, "attachment increases charged storage");
  const { formatUsageBytes } = await import("../web/src/lib/usage-model.ts");
  await until(async () => {
    const text = await page.getByTestId("quota-storage").innerText();
    return text.startsWith(formatUsageBytes(afterMedia) + " из ");
  }, "storage matches authoritative post-attachment usage");
  checkpoint = "usage failure preserves form and snapshot";
  const prior = await page.getByTestId("quota-storage").innerText();
  await page.route("**/api/usage", (route) =>
    route.fulfill({ status: 503, json: { error: "unavailable" } }),
  );
  await page
    .getByRole("button", { name: "Обновить использование", exact: true })
    .click();
  await page.getByText(/Показаны неактуальные данные/).waitFor();
  assert.equal(await page.getByTestId("quota-storage").innerText(), prior);
  assert.equal(
    await page.getByLabel("Название", { exact: true }).inputValue(),
    "Квота " + suffix,
  );
  await page.unroute("**/api/usage");
  await page
    .getByRole("button", { name: "Обновить использование", exact: true })
    .click();
  await until(
    async () =>
      (await page.getByText(/Не удалось обновить использование/).count()) === 0,
    "usage retry",
  );
  checkpoint = "theme save and retained deletion";
  await page.goto(base + "/themes");
  await quota(page, "themes", /0 из 100/);
  const example = await owner.context.request.get(base + "/themes/example.zip");
  assert.ok(example.ok());
  await page.getByLabel("ZIP темы", { exact: true }).setInputFiles({
    name: "quota-theme.zip",
    mimeType: "application/zip",
    buffer: await example.body(),
  });
  await page
    .getByRole("button", { name: "Загрузить и проверить тему", exact: true })
    .click();
  await page
    .getByText("Готова к сохранению", { exact: false })
    .waitFor({ timeout: 30000 });
  assert.match(
    await page.getByTestId("quota-theme-target-revision").innerText(),
    /новое оформление с первой версией/,
  );
  await page
    .getByRole("button", { name: "Сохранить тему", exact: true })
    .click();
  await quota(page, "themes", /1 из 100/);
  const beforeDelete = await api(owner, "GET", "/usage");
  await page.getByRole("button", { name: "Удалить тему", exact: true }).click();
  await page
    .getByRole("button", { name: "Подтвердить удаление темы", exact: true })
    .click();
  await page
    .getByText("У вас пока нет своих тем. Начните с примера ZIP.", {
      exact: true,
    })
    .waitFor();
  await quota(page, "themes", /1 из 100/);
  assert.equal(
    (await api(owner, "GET", "/usage")).storage.used_bytes,
    beforeDelete.storage.used_bytes,
    "deleted theme lost retained charge",
  );
  checkpoint = "account, revoked read and account switch";
  await page.goto(base + "/account");
  await quota(page, "packs", /1 из 200/);
  for (const width of [390, 820, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "quota page overflow",
    );
    await page.screenshot({
      path: output + `/quota-${width}.png`,
      fullPage: true,
    });
  }
  await page.route("**/api/usage", (route) =>
    route.fulfill({ status: 401, json: { error: "unauthorized" } }),
  );
  await page
    .getByRole("button", { name: "Обновить использование", exact: true })
    .click();
  await quota(page, "packs", /Неизвестно/);
  assert.ok(
    !(await page.getByTestId("quota-storage").innerText()).startsWith(
      prior.split(" из ")[0],
    ),
  );
  await page.unroute("**/api/usage");
  owner.needsLogin = true;
  await api(owner, "POST", "/account/logout", {});
  await api(owner, "POST", "/account/login", {
    username: other.username,
    password,
  });
  await page.goto(base + "/account");
  await quota(page, "packs", /0 из 200/);
  await quota(page, "themes", /0 из 100/);
  await quota(page, "storage", /^0 Б/);
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      before_upload: true,
      mutation_refresh: true,
      retained_theme: true,
      stale_error_preserves_form: true,
      unauthorized_clears: true,
      account_isolation: true,
      mobile: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error({ checkpoint, errors, failure: error.stack });
  throw error;
} finally {
  const cleanupErrors = [];
  for (const who of people) {
    try {
      if (who.registered) {
        if (who.needsLogin)
          await api(who, "POST", "/account/login", {
            username: who.username,
            password,
          });
        await api(who, "DELETE", "/account/me", { password });
      }
    } catch (error) {
      cleanupErrors.push(error);
    } finally {
      await who.context.close();
    }
  }
  await browser.close();
  await rm(directory, { recursive: true, force: true });
  if (cleanupErrors.length)
    throw new AggregateError(cleanupErrors, "synthetic account cleanup failed");
}
