import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes, randomUUID } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const base = process.env.BASE_URL || "http://localhost:5173";
const output =
  process.env.ARTIFACT_DIR ||
  fileURLToPath(new URL("../.local/browser", import.meta.url));
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
const contexts = [];
const clients = [];
const pageErrors = [];
const password = randomBytes(20).toString("hex");
const suffix = randomBytes(8).toString("hex");
let ownerContext;
let registered = false;
let checkpoint = "setup";
const rules = {
  preset: "custom",
  selection_seconds: 120,
  answer_seconds: 120,
  special_seconds: 120,
  wager_seconds: 120,
  final_seconds: 120,
  penalties: true,
  buzzer_mode: "first",
  window_ms: 200,
  false_start: true,
  false_start_seconds: 1,
  no_risk_multiplier: 2,
};
async function request(context, method, path, data, token) {
  const response = await context.request.fetch(base + "/api" + path, {
    method,
    data,
    ...(token ? { headers: { Authorization: `Bearer ${token}` } } : {}),
  });
  assert.ok(
    response.ok(),
    `${method} ${path}: ${response.status()} ${await response.text()}`,
  );
  return response.status() === 204 ? null : response.json();
}
async function until(predicate, message) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(`${checkpoint}: ${message}`);
}
async function newClient(credentials, name, context) {
  context ||= await browser.newContext(
    name === "Игрок Альфа"
      ? {
          viewport: { width: 390, height: 844 },
          isMobile: true,
          hasTouch: true,
        }
      : {},
  );
  if (!contexts.includes(context)) contexts.push(context);
  await context.addInitScript(
    ({ credentials }) => {
      sessionStorage.setItem(
        "room:" + credentials.room_id,
        JSON.stringify(credentials),
      );
    },
    { credentials },
  );
  const page = await context.newPage();
  const client = { name, credentials, context, page, state: null, wire: [] };
  clients.push(client);
  page.on("pageerror", (error) =>
    pageErrors.push({ client: name, message: error.message }),
  );
  page.on("websocket", (socket) =>
    new URL(socket.url()).searchParams.get("display") !== "1" && socket.on("framereceived", ({ payload }) => {
      try {
        const message = JSON.parse(payload.toString());
        if (message.state) {
          client.wire.push(message.state);
          if (!client.state || message.state.seq >= client.state.seq)
            client.state = message.state;
        }
      } catch {
        /* Ignore heartbeat frames. */
      }
    }),
  );
  await page.goto(`${base}/rooms/${credentials.room_id}`);
  await until(
    () => client.state?.gameplay?.version === 2,
    `${name} did not receive v2 snapshot`,
  );
  await page.getByRole("status").filter({ hasText: "В сети" }).waitFor();
  return client;
}
function state(client) {
  return client.state.gameplay;
}
async function stage(client, kind) {
  await until(
    () => state(client)?.stage.kind === kind,
    `${client.name}: expected ${kind}, got ${state(client)?.stage.kind}`,
  );
}
function form(client, title) {
  return client.page
    .locator("form.game-action")
    .filter({
      has: client.page.getByRole("heading", { name: title, exact: true }),
    });
}
async function admin(client, title) {
  if (!(await client.page.locator('dialog.game-tools').evaluate(el=>el.open))) await client.page.getByRole('button',{name:'Настройки и управление',exact:true}).click();
  const details = client.page
    .locator("details")
    .filter({
      has: client.page
        .locator("summary")
        .filter({ hasText: /^Управление и апелляции$/ }),
    });
  await details.waitFor();
  if (!(await details.evaluate((el) => el.open)))
    await details.locator("summary").click();
  const target = form(client, title);
  await target.waitFor();
  return target;
}
async function button(client, label) {
  const target=client.page.getByRole("button", { name: label, exact: true });
  if(await client.page.locator('dialog.game-tools').evaluate(el=>el.open) && !(await target.evaluate(el=>!!el.closest('dialog')))) await client.page.getByRole('button',{name:'Закрыть',exact:true}).click();
  await target.click();
}
async function adjust(host, target, delta) {
  const controls = await admin(host, "Изменить счёт");
  await controls
    .getByLabel(/^Участник/)
    .selectOption(target.credentials.participant_id);
  await controls.getByLabel("Изменение очков").fill(String(delta));
  await controls
    .getByLabel("Причина")
    .fill("Начальный счёт для браузерной проверки");
  const previous = host.state.seq;
  await controls
    .getByRole("button", { name: "Изменить счёт", exact: true })
    .click();
  await until(
    () => host.state.seq > previous,
    "score adjustment did not arrive",
  );
}
async function score(client, target, expected) {
  await until(
    () =>
      client.state.participants.find(
        (p) => p.id === target.credentials.participant_id,
      )?.score === expected,
    `${target.name}: score != ${expected}`,
  );
  assert.equal(
    await client.page
      .locator(".score-item")
      .filter({ hasText: target.name })
      .first()
      .locator("strong")
      .innerText(),
    String(expected),
  );
}
function question(type, price, text, right) {
  return {
    id: randomUUID(),
    type,
    price,
    info: {},
    params: [
      {
        name: "question",
        type: "content",
        items: [{ type: "text", value: text }],
      },
    ],
    right: [right],
  };
}
function checkPrivateViews(instanceID, players, watchers) {
  for (const viewer of [...players, ...watchers]) {
    for (const snapshot of viewer.wire.filter(
      (s) => s.gameplay?.question?.instance_id === instanceID,
    )) {
      const game = snapshot.gameplay;
      const isHost =
        snapshot.participants.find((p) => p.id === snapshot.you)?.role ===
        "host";
      for (const submission of game.submissions || []) {
        if (submission.id === snapshot.you || submission.revealed) continue;
        assert.equal(
          Object.hasOwn(submission, "wager"),
          false,
          `${viewer.name}: another player's hidden wager leaked`,
        );
        if (
          !isHost ||
          ["collect_wagers", "present_question", "collect_answers"].includes(
            game.stage.kind,
          )
        ) {
          assert.equal(
            Object.hasOwn(submission, "answer"),
            false,
            `${viewer.name}: another player's hidden answer leaked`,
          );
        }
      }
    }
  }
}
async function closedQuestion(host, players, spectator, label) {
  await stage(host, "collect_wagers");
  const instance = state(host).question.instance_id;
  const wagers = [73, 127];
  const answers = [
    `${label}: закрытый ответ Альфа`,
    `${label}: закрытый ответ Бета`,
  ];
  for (let i = 0; i < players.length; i++) {
    const controls = form(players[i], "Ваша закрытая ставка");
    await controls.getByLabel("Сумма").fill(String(wagers[i]));
    await controls
      .getByRole("button", { name: "Зафиксировать ставку", exact: true })
      .click();
    await until(
      () =>
        state(host).players.some(
          (p) =>
            p.id === players[i].credentials.participant_id && p.wager_submitted,
        ),
      "wager flag did not arrive",
    );
    assert.equal(state(players[i]).self.wager, wagers[i]);
    for (const watcher of [
      host,
      spectator,
      ...players.filter((p) => p !== players[i]),
    ]) {
      await until(
        () =>
          state(watcher).players.some(
            (p) =>
              p.id === players[i].credentials.participant_id &&
              p.wager_submitted,
          ),
        "wager observer did not update",
      );
      assert.equal(
        Object.hasOwn(
          state(watcher).submissions.find(
            (p) => p.id === players[i].credentials.participant_id,
          ),
          "wager",
        ),
        false,
        "closed wager visible",
      );
    }
  }
  await stage(host, "present_question");
  await button(host, "Продолжить →");
  await stage(host, "collect_answers");
  for (let i = 0; i < players.length; i++) {
    const controls = form(players[i], "Ваш ответ");
    await controls.getByLabel("Текст ответа").fill(answers[i]);
    await controls
      .getByRole("button", { name: "Отправить ответ", exact: true })
      .click();
    await until(
      () =>
        state(host).players.some(
          (p) =>
            p.id === players[i].credentials.participant_id &&
            p.answer_submitted,
        ),
      "answer flag did not arrive",
    );
    if (i === 0) {
      for (const watcher of [host, spectator, players[1]]) {
        await until(
          () =>
            state(watcher).players.some(
              (p) =>
                p.id === players[0].credentials.participant_id &&
                p.answer_submitted,
            ),
          "answer observer did not update",
        );
        assert.equal(
          Object.hasOwn(
            state(watcher).submissions.find(
              (p) => p.id === players[0].credentials.participant_id,
            ),
            "answer",
          ),
          false,
          "answer leaked before collection closed",
        );
        assert.equal(
          await watcher.page.getByText(answers[0], { exact: true }).count(),
          0,
          "closed answer rendered",
        );
      }
    }
  }
  await stage(host, "reveal");
  for (let i = 0; i < players.length; i++) {
    const submitted = state(host).submissions.find(
      (p) => p.id === players[i].credentials.participant_id,
    );
    assert.equal(
      submitted.answer,
      answers[i],
      "host cannot review closed collection",
    );
    assert.equal(
      Object.hasOwn(submitted, "wager"),
      false,
      "host learned unrevealed wager",
    );
  }
  for (let i = 0; i < players.length; i++) {
    const target = players.find(
      (p) => p.credentials.participant_id === state(host).stage.turn_id,
    );
    assert.ok(target, "reveal target must be a participant");
    await button(host, `Раскрыть: ${target.name}`);
    await stage(host, "judge");
    await until(
      () =>
        state(spectator).submissions.some(
          (p) => p.id === target.credentials.participant_id && p.revealed,
        ),
      "spectator did not receive reveal",
    );
    const index = players.indexOf(target);
    assert.equal(
      state(spectator).submissions.find(
        (p) => p.id === target.credentials.participant_id,
      ).answer,
      answers[index],
    );
    assert.equal(
      state(spectator).submissions.find(
        (p) => p.id === target.credentials.participant_id,
      ).wager,
      wagers[index],
    );
    await button(host, index === 0 ? "✓ Верно" : "× Неверно");
    await stage(host, i === players.length - 1 ? "resolved" : "reveal");
  }
  checkPrivateViews(instance, players, [host, spectator]);
  return answers;
}
try {
  ownerContext = await browser.newContext();
  contexts.push(ownerContext);
  await request(ownerContext, "POST", "/account/register", {
    username: `rules_${suffix}`,
    password,
    display_name: "Автор правил",
  });
  registered = true;
  const standard = {
    id: randomUUID(),
    name: "Основной раунд",
    type: "standard",
    info: {},
    themes: [
      {
        id: randomUUID(),
        name: "Проверяем правила",
        info: {},
        questions: [
          question(
            "simple",
            100,
            "Обычный вопрос: сколько будет два плюс два?",
            "Четыре — правильный ответ",
          ),
          question(
            "stakeAll",
            200,
            "Закрытый вопрос: назовите столицу Франции.",
            "Париж",
          ),
        ],
      },
    ],
  };
  const final = {
    id: randomUUID(),
    name: "Большой финал",
    type: "final",
    info: {},
    themes: ["Финал Альфа", "Финал Бета", "Финал Гамма"].map((name) => ({
      id: randomUUID(),
      name,
      info: {},
      questions: [
        question(
          "stakeAll",
          700,
          `Первый вопрос: ${name}`,
          "Верный финальный ответ",
        ),
        question(
          "stakeAll",
          800,
          `Второй вопрос: ${name}`,
          "Не должен выбираться",
        ),
      ],
    })),
  };
  const draft = await request(ownerContext, "POST", "/drafts", {
    document: {
      id: randomUUID(),
      name: `Правила ${suffix}`,
      version: "5.0",
      info: {},
      rounds: [standard, final],
    },
  });
  assert.deepEqual(
    (draft.play_issues || []).filter((i) => i.severity === "error"),
    [],
    "fixture is not playable",
  );
  const credentials = await request(ownerContext, "POST", "/rooms", {
    host_name: "Первый ведущий",
    title: `Правила ${suffix}`,
    theme: "classic",
    pack_id: draft.card.id,
    revision: draft.card.revision,
    rules,
  });
  const host = await newClient(credentials, "Первый ведущий", ownerContext);
  const players = [];
  for (const name of ["Игрок Альфа", "Игрок Бета"]) {
    const joined = await request(
      ownerContext,
      "POST",
      `/rooms/${credentials.room_id}/participants`,
      { name, role: "player", invite_token: credentials.invite_token },
    );
    players.push(await newClient(joined, name));
  }
  const joined = await request(
    ownerContext,
    "POST",
    `/rooms/${credentials.room_id}/participants`,
    {
      name: "Новый ведущий",
      role: "spectator",
      invite_token: credentials.invite_token,
    },
  );
  const spectator = await newClient(joined, "Новый ведущий");
  await until(
    () => host.state.participants.length === 4,
    "participants did not join",
  );
  checkpoint = "ordinary retry";
  await adjust(host, players[0], 500);
  await adjust(host, players[1], 500);
  let delayBoard = true;
  await host.page.route("**/api/rooms/*/board", async (route) => {
    if (delayBoard) await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  await button(host, "Начать раунд");
  await button(host, "Проверяем правила, 100 очков");
  delayBoard = false;
  await stage(host, "present_question");
  await stage(players[0], "present_question");
  await button(players[0], "Ждите сигнала");
  await until(() => state(players[0]).players.find((p) => p.id === players[0].credentials.participant_id)?.blocked_until_ms > Date.now(), "false-start was not recorded");
  await button(host, "Продолжить →");
  await stage(players[0], "buzz_open");
  // No commands or reconnect: the server must deliver fresh permission itself.
  await players[0].page.getByRole("button", { name: "Знаю ответ!", exact: true }).waitFor({timeout: 5000});
  assert.equal(
    await host.page
      .getByRole("heading", { name: "Ответы участников", exact: true })
      .count(),
    0,
    "empty answer rows rendered before submissions",
  );
  assert.equal(
    await players[0].page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
    "mobile horizontal overflow",
  );
  const journalBox = await host.page
    .getByRole("button", { name: "Настройки и управление", exact: true })
    .boundingBox();
  assert.ok(
    journalBox.height < 80,
    "sidebar controls stretch with the question column",
  );
  await host.page.screenshot({
    path: output + "/rules-host.png",
    fullPage: true,
  });
  await players[0].page.screenshot({
    path: output + "/rules-player-mobile.png",
    fullPage: true,
  });
  await button(players[0], "Знаю ответ!");
  await stage(host, "collect_answer");
  await button(host, "× Неверно");
  await stage(host, "buzz_open");
  await score(host, players[0], 400);
  await until(
    () =>
      state(players[0]).players.find(
        (p) => p.id === players[0].credentials.participant_id,
      )?.attempted,
    "wrong player not excluded",
  );
  assert.equal(
    await players[0].page
      .getByRole("button", { name: "Знаю ответ!", exact: true })
      .count(),
    0,
    "wrong player can buzz twice",
  );
  for (const viewer of [players[1], spectator])
    assert.equal(
      state(viewer).question.answer,
      undefined,
      "correct answer leaked before retry",
    );
  await button(players[1], "Знаю ответ!");
  await stage(host, "collect_answer");
  await button(host, "✓ Верно");
  await stage(host, "resolved");
  await score(host, players[1], 600);
  assert.equal(
    state(host).chooser_id,
    players[1].credentials.participant_id,
    "chooser did not transfer to winner",
  );
  checkpoint = "closed wager and answer privacy";
  await button(host, "Проверяем правила, 200 очков");
  const secretAnswers = await closedQuestion(
    host,
    players,
    spectator,
    "Обычная общая ставка",
  );
  await score(host, players[0], 473);
  await score(host, players[1], 473);
  checkpoint = "final elimination";
  await host.page.getByLabel(/^Раунд/).selectOption(final.id);
  await button(host, "Начать раунд");
  await stage(host, "eliminate_theme");
  assert.equal(state(host).final.themes.length, 3);
  assert.deepEqual(
    new Set(state(host).final.eligible_ids),
    new Set(players.map((p) => p.credentials.participant_id)),
  );
  for (let remaining = 3; remaining > 1; remaining--) {
    const active = players.find(
      (p) => p.credentials.participant_id === state(host).stage.turn_id,
    );
    assert.ok(active, "final elimination turn was not assigned");
    await stage(active, "eliminate_theme");
    const controls = form(active, "Удалить тему финала");
    await controls
      .getByLabel(/^Тема/)
      .selectOption(state(host).final.themes[0].id);
    await controls
      .getByRole("button", { name: "Удалить тему финала", exact: true })
      .click();
    await until(
      () => state(host).final.themes.length === remaining - 1,
      "theme elimination did not apply",
    );
  }
  await stage(host, "select_final");
  const remaining = state(host).final.themes[0];
  assert.equal(
    await host.page
      .getByRole("button", {
        name: `${remaining.name}, 800 очков`,
        exact: true,
      })
      .isDisabled(),
    true,
    "second question of final theme is selectable",
  );
  await button(host, `${remaining.name}, 700 очков`);
  checkpoint = "final private wagers, reveal and judgement";
  secretAnswers.push(
    ...(await closedQuestion(
      host,
      players,
      spectator,
      "Финальная общая ставка",
    )),
  );
  await score(host, players[0], 546);
  await score(host, players[1], 346);
  checkpoint = "host transfer and old host disconnect";
  await button(host, "Пауза Ⅱ");
  await until(() => host.state.phase === "paused", "pause did not apply");
  const transfer = await admin(host, "Передать роль ведущего");
  await transfer
    .getByLabel(/^Участник/)
    .selectOption(spectator.credentials.participant_id);
  await transfer
    .getByRole("button", { name: "Передать роль ведущего", exact: true })
    .click();
  await until(
    () =>
      spectator.state.participants.find(
        (p) => p.id === spectator.credentials.participant_id,
      )?.role === "host",
    "spectator did not become host",
  );
  await until(
    () =>
      host.state.participants.find(
        (p) => p.id === host.credentials.participant_id,
      )?.role === "spectator",
    "former host role not revoked",
  );
  await host.page.close();
  await button(spectator, "Продолжить ▶");
  await until(
    () => players.every((p) => p.state.phase === "running"),
    "new host did not resume after old host disconnected",
  );
  await button(spectator, "Завершить игру");
  await button(spectator, "Завершить");
  await until(
    () => players.every((p) => p.state.phase === "finished"),
    "new host did not finish game",
  );
  const results = await request(
    ownerContext,
    "GET",
    `/rooms/${credentials.room_id}/results?after_seq=0&limit=100`,
    undefined,
    spectator.credentials.token,
  );
  for (const answer of secretAnswers)
    assert.equal(
      JSON.stringify(results).includes(answer),
      false,
      "private answer leaked through result journal",
    );
  assert.deepEqual(pageErrors, [], "browser page errors");
  console.log(
    JSON.stringify({
      delayed_board_selection: true,
      empty_submission_rows: false,
      sidebar_controls_compact: true,
      mobile_overflow: false,
      false_start_recovers_without_input: true,
      ordinary_retry: true,
      wrong_player_excluded: true,
      chooser_transferred: true,
      private_wagers: true,
      host_answer_privacy: true,
      final_elimination: true,
      final_first_question_only: true,
      final_reveal_judge: true,
      host_transfer_after_disconnect: true,
      final_scores: [546, 346],
      safe_results: true,
      page_errors: 0,
    }),
  );
} catch (error) {
  console.error(
    JSON.stringify({
      checkpoint,
      page_errors: pageErrors,
      stages: clients.map((c) => ({
        name: c.name,
        phase: c.state?.phase,
        stage: c.state?.gameplay?.stage.kind,
        actions: c.state?.gameplay?.allowed_actions.map((a) => a.type),
      })),
    }),
  );
  throw error;
} finally {
  if (registered) {
    const response = await ownerContext.request.delete(
      base + "/api/account/me",
      { data: { password } },
    );
    if (!response.ok())
      console.error(`Synthetic account cleanup failed: ${response.status()}`);
  }
  await Promise.all(contexts.map((context) => context.close()));
  await browser.close();
}
