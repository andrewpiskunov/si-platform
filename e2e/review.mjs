import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
const base = process.env.BASE_URL || "http://localhost:5173";
const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const password = randomBytes(20).toString("hex");
async function request(method, path, data) {
  const response = await context.request.fetch(base + "/api" + path, {
    method,
    data,
  });
  assert.ok(response.ok(), `${method} ${path}: ${response.status()}`);
  return response.status() === 204 ? null : response.json();
}
async function visit(path) {
  await page.goto(base + path);
  await page.waitForLoadState("networkidle");
}
async function until(fn) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("condition timed out");
}
try {
  await request("POST", "/account/register", {
    username: "review_" + randomBytes(8).toString("hex"),
    password,
    display_name: "Проверка",
  });
  const empty = await request("POST", "/drafts", {
    document: { name: "Неполный пакет", version: "5.0", rounds: null },
  });
  await visit("/studio/" + empty.card.id);
  assert.equal(
    await page.getByLabel("Название", { exact: true }).inputValue(),
    "Неполный пакет",
  );
  await page
    .getByRole("button", { name: "Добавить раунд", exact: true })
    .click();
  await page
    .getByLabel("Текст вопроса, фрагмент 1", { exact: true })
    .fill("Два плюс два?");
  await page
    .getByRole("textbox", { name: /^Правильные ответы/ })
    .fill("Четыре");
  await until(() =>
    page
      .getByRole("status")
      .filter({ hasText: "Все изменения сохранены" })
      .count(),
  );
  const repaired = await request("GET", "/drafts/" + empty.card.id);
  assert.equal(repaired.document.rounds.length, 1);
  assert.equal(repaired.issues.length, 0);
  // Null nested collections from an incomplete import remain editable as well.
  const nested = structuredClone(repaired.document);
  nested.rounds[0].themes[0].questions[0].params = null;
  nested.rounds[0].themes[0].questions[0].right = null;
  nested.info = null;
  const partial = await request("POST", "/drafts", { document: nested });
  await visit("/studio/" + partial.card.id);
  assert.equal(
    await page
      .getByRole("textbox", { name: /^Правильные ответы/ })
      .inputValue(),
    "",
  );
  // A pending revision remains withdrawable after a new head is saved.
  await request("POST", `/drafts/${empty.card.id}/publish`, {
    revision: repaired.card.revision,
    rights: {
      license: "CC0",
      source_url: "original",
      attribution: "Проверка",
      confirmed: true,
      allow_download: true,
    },
  });
  await visit("/studio/" + empty.card.id);
  await page.getByLabel("Название", { exact: true }).fill("Новая редакция");
  await until(() =>
    page
      .getByRole("status")
      .filter({ hasText: "Все изменения сохранены" })
      .count(),
  );
  await page.getByText("Публикация в каталоге", { exact: true }).click();
  await page
    .getByRole("button", { name: "Снять публикацию", exact: true })
    .click();
  await page.getByText("Публикация снята.", { exact: true }).waitFor();
  const withdrawn = await request("GET", "/drafts/" + empty.card.id);
  assert.equal(withdrawn.card.pending_revision, 0);
  // Engine incompatibility is visible separately and never prevents SIQ export.
  const zero = structuredClone(repaired.document);
  zero.rounds[0].themes[0].questions[0].price = 100001;
  const invalidGame = await request("POST", "/drafts", { document: zero });
  await visit("/studio/" + invalidGame.card.id);
  await page.getByText(/Готовность к игре/).waitFor();
  await page.getByText("Сыграть в этот пакет", { exact: true }).click();
  assert.equal(
    await page
      .getByRole("button", { name: "Создать комнату →", exact: true })
      .isDisabled(),
    true,
  );
  // Full session shell shares the room palette, and SPA navigation resets it.
  const room = await request("POST", "/rooms", {
    host_name: "Ведущий",
    theme: "pirate",
  });
  await visit("/rooms/" + room.room_id + "#invite=" + room.invite_token);
  // Use the normal join UI as a spectator; theme comes from authenticated board.
  if (await page.getByLabel("Ваше имя", { exact: true }).count()) {
    await page.getByLabel("Ваше имя", { exact: true }).fill("Зритель");
    await page
      .getByRole("button", { name: "Войти в комнату →", exact: true })
      .click();
  }
  await until(
    async () =>
      (await page
        .locator(".topbar")
        .evaluate((e) =>
          getComputedStyle(e).getPropertyValue("--bg").trim(),
        )) === "#102a29",
  );
  assert.equal(
    await page
      .locator(".site-footer")
      .evaluate((e) => getComputedStyle(e).getPropertyValue("--bg").trim()),
    "#102a29",
  );
  await page
    .getByRole("navigation", { name: "Основная навигация" })
    .getByRole("link", { name: "Мастерская", exact: true })
    .click();
  await page.waitForURL("**/studio");
  await until(
    async () =>
      (await page
        .locator(".topbar")
        .evaluate((e) =>
          getComputedStyle(e).getPropertyValue("--bg").trim(),
        )) === "#101725",
  );
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      diagnostic_draft_repair: true,
      null_question: true,
      withdraw_after_edit: true,
      engine_diagnostics: true,
      whole_session_theme: true,
      theme_reset: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error(
    await page
      .locator("main")
      .innerText()
      .catch(() => ""),
  );
  console.error(errors);
  throw error;
} finally {
  try {
    await context.request.delete(base + "/api/account/me", {
      data: { password },
    });
  } catch {}
  await context.close();
  await browser.close();
}
