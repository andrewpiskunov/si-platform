import { chromium } from "playwright";
import assert from "node:assert/strict";
for (const endpoint of ["roster", "results"]) {
  const browser = await chromium.launch();
  const base = process.env.BASE_URL || "http://localhost:5173";
  const context = await browser.newContext();
  const page = await context.newPage();
  const room = "a".repeat(32),
    old = "b".repeat(32),
    fresh = "c".repeat(32);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.stack || e.message));
  await context.addInitScript(
    ({ room, old, fresh, endpoint }) => {
      const token = "d".repeat(64);
      sessionStorage.setItem(
        "room:" + room,
        JSON.stringify({ room_id: room, participant_id: old, token }),
      );
      window.oldRequestSettled = false;
      const nativeFetch = window.fetch;
      window.fetch = async (...args) => {
        const oldRequest = String(args[0]).includes("/" + endpoint);
        try {
          const response = await nativeFetch(...args);
          if (oldRequest) {
            const json = response.json.bind(response);
            response.json = async () => {
              try {
                return await json();
              } finally {
                setTimeout(() => {
                  window.oldRequestSettled = true;
                }, 0);
              }
            };
          }
          return response;
        } catch (error) {
          if (oldRequest)
            setTimeout(() => {
              window.oldRequestSettled = true;
            }, 0);
          throw error;
        }
      };
      const Native = window.WebSocket;
      window.reviewSockets = [];
      window.WebSocket = class {
        static OPEN = 1;
        constructor(url) {
          if (!String(url).includes("/api/rooms/")) return new Native(url);
          this.readyState = 0;
          window.reviewSockets.push(this);
          setTimeout(() => {
            this.readyState = 1;
            this.onopen?.();
          }, 10);
        }
        send(raw) {
          const auth = JSON.parse(raw);
          if (auth.type !== "authenticate") return;
          const you = auth.token === token ? old : fresh;
          const state = {
            room_id: room,
            seq: you === old ? 2 : 4,
            phase: "lobby",
            you,
            participants: [
              { id: "e".repeat(32), name: "Host", role: "host", score: 0 },
              {
                id: you,
                name: you === old ? "Old" : "New",
                role: "player",
                score: 0,
              },
            ],
            settings: {
              visibility: "public",
              title: "Review room",
              voice_url: "",
            },
            gameplay: {
              version: 2,
              rules_version: 1,
              rules: {
                preset: "classic",
                selection_seconds: 30,
                answer_seconds: 10,
                special_seconds: 30,
                wager_seconds: 30,
                final_seconds: 60,
                penalties: true,
                buzzer_mode: "first",
                window_ms: 200,
                false_start: true,
                false_start_seconds: 3,
                no_risk_multiplier: 2,
              },
              chooser_id: you,
              server_time_ms: Date.now(),
              stage: { id: "stage-1", kind: "select_question", expired: false },
              players: [],
              self: {},
              submissions: [],
              decisions: [],
              appeals: [],
              allowed_actions: [],
              readiness: [],
            },
          };
          setTimeout(
            () =>
              this.onmessage?.({
                data: JSON.stringify({ v: 1, type: "snapshot", state }),
              }),
            10,
          );
        }
        close() {
          this.readyState = 3;
          this.onclose?.();
        }
      };
    },
    { room, old, fresh, endpoint },
  );
  let delayedRoster, routeReady;
  const routeRequested = new Promise((resolve) => {
    routeReady = resolve;
  });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/" + endpoint)) {
      delayedRoster = route;
      routeReady();
      return;
    }
    if (url.pathname.endsWith("/session"))
      return route.fulfill({ status: 204 });
    if (url.pathname.endsWith("/participants"))
      return route.fulfill({
        status: 201,
        json: { room_id: room, participant_id: fresh, token: "f".repeat(64) },
      });
    if (url.pathname.endsWith("/board"))
      return route.fulfill({
        status: 200,
        json: { room_id: room, rounds: [] },
      });
    if (url.pathname.endsWith("/appearance"))
      return route.fulfill({
        status: 200,
        json: { status: "ready", appearance: { builtin: "classic" } },
      });
    return route.fulfill({ status: 401, json: { error: "unauthorized" } });
  });
  try {
    await page.goto(base + "/rooms/" + room);
    await page.getByRole("status").filter({ hasText: "В сети" }).waitFor();
    if (endpoint === "roster") {
      await page.getByText("История состава", { exact: true }).click();
      await page
        .getByRole("button", { name: "Загрузить состав", exact: true })
        .click();
    } else
      await page
        .getByRole("button", { name: "Журнал и результаты", exact: true })
        .click();
    await routeRequested;
    if (!delayedRoster) throw Error("Roster request absent");
    await page.evaluate(() =>
      window.reviewSockets[0].onmessage({
        data: JSON.stringify({ v: 1, type: "error", code: "unauthorized" }),
      }),
    );
    await page
      .getByRole("heading", { name: "Доступ к комнате прекращён", exact: true })
      .waitFor();
    await page
      .getByRole("button", { name: "Войти заново по приглашению", exact: true })
      .click();
    await page.getByLabel("Ваше имя", { exact: true }).fill("New");
    await page
      .getByRole("button", { name: "Войти в комнату →", exact: true })
      .click();
    await page.getByRole("status").filter({ hasText: "В сети" }).waitFor();
    assert.equal(
      await page.evaluate(
        (room) =>
          JSON.parse(sessionStorage.getItem("room:" + room)).participant_id,
        room,
      ),
      fresh,
    );
    await delayedRoster
      .fulfill({ status: 401, json: { error: "unauthorized" } })
      .catch(() => {});
    await page.waitForFunction(() => window.oldRequestSettled);
    await page.evaluate(() =>
      window.reviewSockets[0].onmessage?.({
        data: JSON.stringify({ v: 1, type: "error", code: "unauthorized" }),
      }),
    );
    assert.equal(
      await page
        .getByRole("heading", {
          name: "Доступ к комнате прекращён",
          exact: true,
        })
        .count(),
      0,
      "stale roster 401 revoked new identity",
    );
    assert.equal(
      await page.evaluate(
        (room) =>
          JSON.parse(sessionStorage.getItem("room:" + room))?.participant_id,
        room,
      ),
      fresh,
      "new room credentials lost",
    );
    assert.deepEqual(errors, []);
    console.log(
      `PASS stale ${endpoint} response and stopped socket cannot revoke rejoined identity`,
    );
  } finally {
    await browser.close();
  }
}
