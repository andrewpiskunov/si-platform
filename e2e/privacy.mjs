import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes, createHash } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
const base = process.env.BASE_URL || "http://localhost:5173";
const browser = await chromium.launch(),
  context = await browser.newContext();
const page = await context.newPage(),
  errors = [];
context.on("page", (p) => p.on("pageerror", (e) => errors.push(e.message)));
page.on("pageerror", (e) => errors.push(e.message));
const directory = await mkdtemp(join(tmpdir(), "si-privacy-"));
const username = "privacy_" + randomBytes(6).toString("hex"),
  password = randomBytes(20).toString("hex");
let registered = false,
  checkpoint = "setup";
async function api(method, path, data) {
  const deadline = Date.now() + 60000;
  for (;;) {
    const r = await context.request.fetch(base + "/api" + path, {
      method,
      data,
    });
    if (
      r.status() === 429 &&
      path.startsWith("/account/") &&
      Date.now() < deadline
    ) {
      const seconds = Number(r.headers()["retry-after"]);
      assert.ok(seconds > 0 && seconds <= 10);
      await new Promise((resolve) => setTimeout(resolve, seconds * 1000));
      continue;
    }
    assert.ok(r.ok(), `${method} ${path}: ${r.status()}`);
    return r.status() === 204 ? null : r.json();
  }
}
async function until(fn, message) {
  const end = Date.now() + 25000;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw Error(checkpoint + ": " + message);
}
async function download() {
  let finish;
  const promise = new Promise((resolve) => (finish = resolve));
  const listener = (d) => finish(d);
  const newPage = (p) => p.on("download", listener);
  page.on("download", listener);
  context.on("page", newPage);
  let timer;
  try {
    await page
      .getByRole("link", { name: "↓ Скачать ZIP", exact: true })
      .click();
    const d = await Promise.race([
      promise,
      new Promise(
        (_, reject) =>
          (timer = setTimeout(
            () => reject(Error("native download timeout")),
            30000,
          )),
      ),
    ]);
    assert.equal(d.suggestedFilename(), "account-data.zip");
    const target = join(directory, randomBytes(4).toString("hex") + ".zip");
    await d.saveAs(target);
    const { stdout } = await promisify(execFile)(
      "python3",
      [
        "-c",
        `import json,sys,zipfile,hashlib
with zipfile.ZipFile(sys.argv[1]) as z:
 print(json.dumps({'metadata':'\\n'.join(z.read(n).decode('utf-8') for n in z.namelist() if not n.startswith('objects/')), 'objects':{n:hashlib.sha256(z.read(n)).hexdigest() for n in z.namelist() if n.startswith('objects/')}}))`,
        target,
      ],
      { maxBuffer: 4 * 1024 * 1024 },
    );
    return JSON.parse(stdout);
  } finally {
    clearTimeout(timer);
    page.off("download", listener);
    context.off("page", newPage);
  }
}
try {
  await api("POST", "/account/register", {
    username,
    password,
    display_name: "Privacy " + username,
  });
  registered = true;
  await page.goto(base + "/studio");
  await page
    .getByTestId("quota-packs")
    .filter({ hasText: "0 из 200" })
    .waitFor();
  await page
    .getByRole("button", { name: "＋ Новый пакет", exact: true })
    .click();
  await page.waitForURL(/\/studio\/[a-f0-9]{32}$/);
  const packID = new URL(page.url()).pathname.split("/").pop();
  const title = "Own retained " + username;
  await page.getByLabel("Название", { exact: true }).fill(title);
  await until(async () => {
    const d = await api("GET", "/drafts/" + packID);
    return d.card.name === title && d.card.revision >= 2;
  }, "draft autosave");
  await api("DELETE", "/drafts/" + packID);
  checkpoint = "retained theme files";
  await page.goto(base + "/themes");
  await page
    .getByTestId("quota-themes")
    .filter({ hasText: "0 из 100" })
    .waitFor();
  const example = await context.request.get(base + "/themes/example.zip");
  assert.ok(example.ok());
  const original = await example.body(),
    originalKey = createHash("sha256").update(original).digest("hex");
  await page
    .getByLabel("ZIP темы", { exact: true })
    .setInputFiles({
      name: "privacy-theme.zip",
      mimeType: "application/zip",
      buffer: original,
    });
  await page
    .getByRole("button", { name: "Загрузить и проверить тему", exact: true })
    .click();
  await page
    .getByText("Готова к сохранению", { exact: false })
    .waitFor({ timeout: 30000 });
  await page
    .getByRole("button", { name: "Сохранить тему", exact: true })
    .click();
  await page
    .getByTestId("quota-themes")
    .filter({ hasText: "1 из 100" })
    .waitFor();
  const themeID = (await api("GET", "/themes")).themes[0].id;
  await api("DELETE", "/themes/" + themeID);
  checkpoint = "whole archive";
  await page.goto(base + "/account");
  await page
    .getByTestId("quota-packs")
    .filter({ hasText: "0 из 200" })
    .waitFor();
  let archive = await download();
  assert.ok(
    archive.metadata.includes(title) &&
      archive.metadata.includes(packID) &&
      archive.metadata.includes(themeID),
  );
  assert.equal(
    archive.objects["objects/" + originalKey],
    originalKey,
    "retained original bytes missing",
  );
  for (const [name, hash] of Object.entries(archive.objects))
    assert.equal(name, "objects/" + hash, "object integrity");
  for (const forbidden of [
    "password_hash",
    "token_hash",
    "credential_id",
    password,
  ])
    assert.ok(!archive.metadata.includes(forbidden), "secret in archive");
  checkpoint = "selected metadata";
  await page.getByLabel(/^Состав выгрузки/).selectOption("pack");
  const selector = page.getByLabel(/^Материал \(включая удалённые\)/);
  await selector
    .locator(`option[value="${packID}"]`)
    .waitFor({ state: "attached" });
  assert.match(await selector.innerText(), /удалён/);
  await selector.selectOption(packID);
  await page
    .getByLabel("Только метаданные, без файлов", { exact: true })
    .check();
  archive = await download();
  assert.ok(archive.metadata.includes(packID));
  assert.ok(!archive.metadata.includes(themeID));
  assert.deepEqual(archive.objects, {});
  checkpoint = "picker failure and recovery";
  await page.route("**/api/privacy/subjects?kind=theme", (route) =>
    route.fulfill({ status: 503, json: { error: "unavailable" } }),
  );
  await page.getByLabel(/^Состав выгрузки/).selectOption("theme");
  await page
    .getByRole("button", { name: "Повторить загрузку списка", exact: true })
    .waitFor();
  assert.equal(
    await page
      .getByLabel("Только метаданные, без файлов", { exact: true })
      .isChecked(),
    true,
  );
  await page.unroute("**/api/privacy/subjects?kind=theme");
  await page
    .getByRole("button", { name: "Повторить загрузку списка", exact: true })
    .click();
  await selector
    .locator(`option[value="${themeID}"]`)
    .waitFor({ state: "attached" });
  await selector.selectOption(themeID);
  for (const width of [390, 820, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    const layout = await page.evaluate(() => ({
      width: innerWidth,
      scroll: document.documentElement.scrollWidth,
      overflow: [...document.querySelectorAll("main *")]
        .filter((e) => e.getBoundingClientRect().right > innerWidth + 1)
        .slice(0, 8)
        .map((e) => ({
          tag: e.tagName,
          class: e.className,
          right: e.getBoundingClientRect().right,
        })),
    }));
    assert.ok(layout.scroll <= width + 1, JSON.stringify(layout));
  }
  const errorPage = await context.newPage();
  const response = await errorPage.goto(
    base + "/api/privacy/export?metadata_only=invalid",
  );
  assert.equal(response.status(), 400);
  await errorPage
    .getByRole("heading", { name: "Не удалось подготовить выгрузку" })
    .waitFor();
  await errorPage.getByRole("link", { name: "Вернуться в аккаунт" }).waitFor();
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      native_zip: true,
      retained_pack_versions: true,
      retained_theme_original: true,
      object_hashes: true,
      metadata_filter: true,
      local_retry: true,
      native_error: true,
      mobile: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error({
    checkpoint,
    errors,
    url: page.url(),
    labels: await page.locator("label").allTextContents(),
    message: error.message,
  });
  throw error;
} finally {
  try {
    if (registered) await api("DELETE", "/account/me", { password });
  } finally {
    await browser.close();
    await rm(directory, { recursive: true, force: true });
  }
}
