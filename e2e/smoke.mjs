import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { mkdir } from "node:fs/promises";

const base = process.env.BASE_URL || "http://localhost:5173";
const output = process.env.ARTIFACT_DIR || "../.local/browser";
await mkdir(output, { recursive: true });
const browser = await chromium.launch();
const contexts = [];
const errors = [];
let owner;
const password = randomBytes(20).toString("hex");
const username = `e2e_${randomBytes(8).toString("hex")}`;
async function context(options = {}) {
  const ctx = await browser.newContext(options);
  contexts.push(ctx);
  const page = await ctx.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  return page;
}
async function settled(page, path) {
  await page.goto(base + path);
  await page.waitForLoadState("networkidle");
}
async function until(condition, message) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(message);
}
try {
  owner = await context();
  await settled(owner, "/login");
  await owner.getByRole("button", { name: "Регистрация", exact: true }).click();
  await owner.getByLabel("Логин").fill(username);
  await owner.getByLabel("Имя в игре").fill("Автор теста");
  await owner.getByLabel("Пароль").fill(password);
  await owner
    .getByRole("button", { name: "Создать аккаунт", exact: true })
    .click();
  await owner.waitForURL("**/studio");
  await owner
    .getByRole("button", { name: "＋ Новый пакет", exact: true })
    .click();
  await owner.waitForURL(/\/studio\/[a-f0-9]{32}$/);
  const editorURL = owner.url();
  const title = `Проверка ${username}`;
  await owner.getByLabel("Название", { exact: true }).fill(title);
  await owner
    .getByLabel("Текст вопроса, фрагмент 1", { exact: true })
    .fill("Сколько будет два плюс два?");
  await owner
    .getByRole("textbox", { name: /^Правильные ответы/ })
    .fill("Четыре");
  await until(
    () =>
      owner
        .getByRole("status")
        .filter({ hasText: "Все изменения сохранены" })
        .count(),
    "autosave did not complete",
  );
  await owner.reload();
  await owner.getByLabel("Название", { exact: true }).waitFor();
  assert.equal(
    await owner.getByLabel("Название", { exact: true }).inputValue(),
    title,
  );
  assert.equal(
    await owner
      .getByRole("textbox", { name: /^Правильные ответы/ })
      .inputValue(),
    "Четыре",
  );
  await owner.screenshot({ path: `${output}/editor.png`, fullPage: true });
  const downloadEvent = owner.waitForEvent("download");
  await owner.getByRole("button", { name: "↓ SIQ", exact: true }).click();
  const download = await downloadEvent;
  const archivePath = `${output}/roundtrip.siq`;
  await download.saveAs(archivePath);
  assert.equal(await download.failure(), null);
  await owner.getByText("Сыграть в этот пакет", { exact: true }).click();
  await owner
    .getByLabel("Название комнаты", { exact: true })
    .fill("Браузерная проверка");
  await owner.getByRole("button", { name: process.env.SMOKE_THEME === "classic" ? "Эфир" : "Пираты", exact: true }).click();
  await owner
    .getByRole("button", { name: "Создать комнату →", exact: true })
    .click();
  await owner.waitForURL(/\/rooms\/[a-f0-9]{32}#invite=/);
  const invitation = owner.url();
  const players = [];
  for (let i = 0; i < 12; i++) {
    const player = await context(
      i === 0
        ? {
            viewport: { width: 390, height: 844 },
            isMobile: true,
            hasTouch: true,
          }
        : {},
    );
    players.push(player);
    await settled(player, invitation.slice(base.length));
    await player.getByLabel("Ваше имя", { exact: true }).fill(`Игрок ${i + 1}`);
    await player
      .getByRole("button", { name: "Войти в комнату →", exact: true })
      .click();
    await player
      .getByRole("status")
      .filter({ hasText: "Собираемся" })
      .waitFor();
  }
  assert.equal(await owner.locator('.lobby-seat .participant-avatar').count(), 12);
  for (const avatar of await owner.locator('.lobby-seat .participant-avatar').all()) {
    await until(() => avatar.evaluate(el => el.complete && el.naturalWidth > 0), 'avatar did not load');
  }
  const start = owner.getByRole("button", { name: "Начать раунд", exact: true });
  assert.equal(await start.isDisabled(), true);
  assert.equal(await owner.locator('.lobby-seat').count(), 12);
  for (const player of players) await player.getByRole('button', {name: 'Готов к игре', exact: true}).click();
  await until(() => start.isEnabled(), 'ready players did not unlock start');
  await players[0].getByRole('button', {name: 'Снять готовность', exact: true}).click();
  await until(() => start.isDisabled(), 'unready did not lock start');
  await players[0].getByRole('button', {name: 'Готов к игре', exact: true}).click();
  await players[0].getByRole('button', {name: 'Снять готовность', exact: true}).waitFor();
  await players[0].reload();
  await players[0].getByRole('button', {name: 'Снять готовность', exact: true}).waitFor();
  await owner.screenshot({path: `${output}/lobby.png`, fullPage: true});
  await players[0].screenshot({path: `${output}/lobby-mobile.png`, fullPage: true});
  await start.click();
  await owner.getByRole("status").filter({ hasText: "Идёт игра" }).waitFor();
  await owner
    .getByRole("button", { name: "Новая тема, 100 очков", exact: true })
    .click();
  await owner.getByRole("button", { name: "Открыть кнопку", exact: true }).click();
  await players[0]
    .getByRole("button", { name: "Знаю ответ!", exact: true })
    .waitFor();
  assert.equal(
    await players[0].getByText("Четыре", { exact: true }).count(),
    0,
    "answer leaked before judging",
  );
  assert.equal(
    await players[0].evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
    "mobile horizontal overflow",
  );
  await owner.screenshot({ path: `${output}/host.png`, fullPage: true });
  await players[0].screenshot({
    path: `${output}/player-mobile.png`,
    fullPage: true,
  });
  await players[0]
    .getByRole("button", { name: "Знаю ответ!", exact: true })
    .click();
  await owner.getByRole("button", { name: /^✓ Верно/ }).click();
  await until(
    async () =>
      (await players[0]
        .locator(".score-item")
        .filter({ hasText: "Игрок 1 (вы)" })
        .locator("strong")
        .innerText()) === "100",
    "score did not arrive",
  );
  await players[0].reload();
  await until(async () => {
    const row = players[0]
      .locator(".score-item")
      .filter({ hasText: "Игрок 1 (вы)" })
      .locator("strong");
    return (await row.count()) > 0 && (await row.innerText()) === "100";
  }, "reconnect lost score");
  await owner
    .getByRole("button", { name: "Завершить игру", exact: true })
    .click();
  await owner.getByRole("button", { name: "Завершить", exact: true }).click();
  await players[0]
    .getByRole("status")
    .filter({ hasText: "Игра завершена" })
    .waitFor();
  // A new browser upload goes through chunked HTTP and the durable importer.
  await settled(owner, "/studio");
  await owner
    .getByLabel("Файл SIQ", { exact: true })
    .setInputFiles(archivePath);
  await owner
    .getByRole("button", { name: "Загрузить SIQ ↑", exact: true })
    .click();
  await owner.waitForURL(/\/studio\/[a-f0-9]{32}$/, { timeout: 20000 });
  assert.notEqual(owner.url(), editorURL);
  await owner.getByLabel("Название", { exact: true }).waitFor();
  assert.equal(
    await owner.getByLabel("Название", { exact: true }).inputValue(),
    title,
  );
  await owner.getByText("Файлы и управление", { exact: true }).click();
  await owner
    .getByRole("link", { name: "Скачать исходный SIQ", exact: true })
    .waitFor();
  assert.deepEqual(errors, [], "browser page errors");
  console.log(
    JSON.stringify({
      registration: true,
      autosave_reload: true,
      siq_roundtrip: true,
      players: 12,
      answer_private: true,
      mobile_overflow: false,
      reconnect_score: 100,
      finished: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error(await owner?.locator("main").innerText().catch(() => ""));
  console.error(errors);
  await owner?.screenshot({ path: `${output}/smoke-failure.png`, fullPage: true });
  throw error;
} finally {
  if (owner) {
    try {
      await owner.request.delete(base + "/api/account/me", {
        data: { password },
      });
    } catch {}
  }
  await Promise.all(contexts.map((c) => c.close()));
  await browser.close();
}
