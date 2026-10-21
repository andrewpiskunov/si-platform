import { chromium } from "playwright";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { readFile, writeFile, mkdtemp, rm, mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { manifestStyle } from "../web/src/lib/appearance.ts";

const base = process.env.BASE_URL || "http://localhost:5173";
const output =
  process.env.ARTIFACT_DIR ||
  fileURLToPath(new URL("../.local/browser", import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), "si-theme-regressions-"));
const browser = await chromium.launch(),
  context = await browser.newContext();
const password = randomBytes(20).toString("hex");
const pageErrors = [],
  failures = [],
  report = {};
let registered = false;
await mkdir(output, { recursive: true });
async function request(method, path, data, token) {
  const response = await context.request.fetch(base + "/api" + path, {
    method,
    data,
    ...(token ? { headers: { Authorization: `Bearer ${token}` } } : {}),
  });
  assert.ok(
    response.ok(),
    `${method} ${path}: ${response.status()} ${await response.text()}`,
  );
  return response.json();
}
async function until(predicate, label, timeout = 8000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(label);
}
async function check(name, fn) {
  try {
    report[name] = await fn();
    console.log("[theme-regressions] PASS " + name);
  } catch (error) {
    failures.push({ name, message: error.message });
    console.log("[theme-regressions] FAIL " + name + ": " + error.message);
  }
}
async function createRoom(title) {
  return request("POST", "/rooms", {
    host_name: "Ведущий проверки",
    title,
    theme: "classic",
  });
}
async function joinPlayer(creds, name) {
  return request("POST", `/rooms/${creds.room_id}/participants`, {
    name,
    role: "player",
    invite_token: creds.invite_token,
  });
}
async function roomPage(creds, failFirst = true) {
  const page = await context.newPage(),
    client = { page, state: null, snapshots: 0, requests: 0 };
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.addInitScript((credentials) => {
    sessionStorage.setItem(
      "room:" + credentials.room_id,
      JSON.stringify(credentials),
    );
    const Native = WebSocket;
    window.__themeRegressionSockets = [];
    window.WebSocket = class extends Native {
      constructor(...args) {
        super(...args);
        window.__themeRegressionSockets.push(this);
      }
    };
  }, creds);
  page.on("websocket", (socket) =>
    socket.on("framereceived", ({ payload }) => {
      try {
        const message = JSON.parse(payload.toString());
        if (message.type === "snapshot") client.snapshots++;
        if (message.state) client.state = message.state;
      } catch {}
    }),
  );
  await page.route(`**/api/rooms/${creds.room_id}/board`, async (route) => {
    client.requests++;
    if (failFirst && client.requests === 1)
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: '{"error":"unavailable"}',
      });
    else await route.continue();
  });
  await page.goto(base + "/rooms/" + creds.room_id);
  await until(
    () => client.state?.gameplay?.version === 2,
    "room did not receive snapshot",
  );
  if (failFirst)
    await page
      .getByText("Сервис временно недоступен. Попробуйте ещё раз.", {
        exact: true,
      })
      .waitFor();
  return client;
}
async function roundOptions(page) {
  return page.getByRole("combobox", { name: /^Раунд/ }).locator("option").count();
}
async function lightTheme(accent) {
  const manifest = {
    schema_version: 1,
    name: accent === "#FFFFFF" ? "Светлая белая" : "Светлая чёрная",
    author: "Проверка регрессий",
    provenance: "Только палитра, собственные тестовые данные.",
    font: "sans",
    palette: {
      background: "#FFFFFF",
      surface: "#FFFFFF",
      surface_alt: "#FFFFFF",
      border: "#000000",
      text: "#000000",
      muted: "#000000",
      accent,
      accent_text: accent === "#FFFFFF" ? "#000000" : "#FFFFFF",
      danger: "#000000",
      success: "#000000",
      focus: "#000000",
    },
    cards: { radius: 12, border_width: 1 },
    buttons: { radius: 8, border_width: 1 },
  };
  const source = join(temporary, "theme.json"),
    archive = join(temporary, "theme.zip");
  await writeFile(source, JSON.stringify(manifest));
  await promisify(execFile)("python3", [
    "-c",
    'import sys,zipfile\nwith zipfile.ZipFile(sys.argv[2],"w",zipfile.ZIP_DEFLATED) as z:z.write(sys.argv[1],"theme.json")',
    source,
    archive,
  ]);
  const bytes = await readFile(archive);
  let upload = await request("POST", "/theme-uploads", {
    name: "light-theme.zip",
    size: bytes.length,
  });
  const patch = await context.request.patch(
    base + `/api/uploads/${upload.id}`,
    {
      headers: {
        "Content-Type": "application/octet-stream",
        "Upload-Offset": "0",
      },
      data: bytes,
    },
  );
  assert.equal(patch.status(), 200, await patch.text());
  upload = await request("POST", `/uploads/${upload.id}/complete`, {});
  await until(
    async () => {
      upload = await request("GET", `/uploads/${upload.id}`);
      assert.notEqual(upload.status, "failed", JSON.stringify(upload.issues));
      return upload.status === "ready";
    },
    "palette-only theme did not prepare",
    20000,
  );
  const version = await request("POST", "/themes", { upload_id: upload.id });
  assert.equal(version.prepared.manifest.palette.accent, accent);
  return version.prepared.manifest;
}
async function paletteContrast() {
  const css = await readFile(
      new URL("../web/src/app.css", import.meta.url),
      "utf8",
    ),
    results = [];
  for (const accent of ["#FFFFFF", "#000000"]) {
    const manifest = await lightTheme(accent),
      page = await context.newPage();
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.setContent(
      `<style>${css}</style><div class="app-shell" data-theme="custom" style="${manifestStyle(manifest)}"><div class="page"><div class="brand-mark">С</div><p class="eyebrow">Тема</p><div class="hero"><h1><span>Вопрос</span></h1></div><div class="card-symbol"><span>? / !</span></div><div class="empty-symbol">?</div><a class="text-link" href="#">Ссылка</a><div class="hero-board-cells">${Array.from({ length: 9 }, () => "<span>100</span>").join("")}</div><button class="board-cell">200</button><div class="question-price">300 очков</div><div class="score-item"><span>Игрок</span><strong>500</strong></div><div class="question-pills"><button class="selected">1</button></div><div class="game-timer"><strong>02:00</strong></div><button class="primary">Начать игру</button><button class="buzz">Знаю ответ!</button></div></div>`,
    );
    const colors = await page.evaluate(() => {
      const selectors = [
        ".brand-mark",
        ".eyebrow",
        ".hero h1 span",
        ".card-symbol > span:first-child",
        ".empty-symbol",
        ".text-link",
        ".hero-board-cells span",
        ".hero-board-cells span:nth-child(5)",
        ".board-cell",
        ".question-price",
        ".score-item strong",
        ".question-pills button.selected",
        ".game-timer strong",
        "button.primary",
        "button.buzz",
      ];
      function rgb(value) {
        return (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
      }
      function luminance(value) {
        return rgb(value)
          .map((channel) => {
            const n = channel / 255;
            return n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4;
          })
          .reduce((sum, n, i) => sum + n * [0.2126, 0.7152, 0.0722][i], 0);
      }
      return selectors.map((selector) => {
        const el = document.querySelector(selector),
          color = getComputedStyle(el).color;
        let node = el,
          background;
        while (node) {
          background = getComputedStyle(node).backgroundColor;
          if (background !== "rgba(0, 0, 0, 0)" && background !== "transparent")
            break;
          node = node.parentElement;
        }
        const a = luminance(color),
          b = luminance(background || "rgb(255, 255, 255)");
        return {
          selector,
          color,
          background,
          contrast: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05),
        };
      });
    });
    await page.screenshot({
      path: join(
        output,
        accent === "#FFFFFF"
          ? "theme-light-white.png"
          : "theme-light-black.png",
      ),
      fullPage: true,
    });
    results.push({ accent, colors });
    await page.close();
  }
  const bad = results.flatMap((result) =>
    result.colors
      .filter((color) => color.contrast < 4.5)
      .map((color) => ({ ...color, accent: result.accent })),
  );
  assert.equal(
    bad.length,
    0,
    `accepted palette contrast below 4.5: ${bad.map((value) => value.selector + "=" + value.contrast).join(", ")}`,
  );
  for (const result of results) {
    for (const selector of [
      "button.primary",
      "button.buzz",
      ".hero-board-cells span:nth-child(5)",
    ]) {
      const color = result.colors.find((value) => value.selector === selector);
      assert.equal(
        color.background,
        result.accent === "#FFFFFF" ? "rgb(255, 255, 255)" : "rgb(0, 0, 0)",
        selector + " must retain accent background",
      );
    }
  }
  return results.map((result) => ({
    accent: result.accent,
    minimumContrast: Math.min(...result.colors.map((color) => color.contrast)),
    selectors: result.colors.length,
  }));
}
try {
  await request("POST", "/account/register", {
    username: "regress_" + randomBytes(6).toString("hex"),
    password,
    display_name: "Проверка тем",
  });
  registered = true;
  if (process.env.REGRESSION_CASE !== "contrast") {
    await check("board_join_snapshot", async () => {
      const creds = await createRoom("Табло после присоединения"),
        client = await roomPage(creds);
      try {
        await joinPlayer(creds, "Первый");
        await until(
          () => client.state.participants.some((p) => p.name === "Первый"),
          "join snapshot missing",
        );
        await until(
          async () => (await roundOptions(client.page)) > 0,
          `same-key join did not retry failed board (${client.requests} requests)`,
          3000,
        );
        const loaded = client.requests;
        await joinPlayer(creds, "Второй");
        await until(
          () => client.state.participants.some((p) => p.name === "Второй"),
          "second join missing",
        );
        await client.page.waitForTimeout(250);
        assert.equal(
          client.requests,
          loaded,
          "successful unchanged board fetched again",
        );
        assert.equal(loaded, 2);
        return {
          requests: client.requests,
          roundOptions: await roundOptions(client.page),
        };
      } finally {
        await client.page.close();
      }
    });
    await check("board_explicit_retry", async () => {
      const creds = await createRoom("Табло по кнопке"),
        client = await roomPage(creds);
      try {
        const snapshots = client.snapshots;
        await client.page
          .getByRole("button", {
            name: "Повторить загрузку табло",
            exact: true,
          })
          .click({ timeout: 3000 });
        await until(
          async () => (await roundOptions(client.page)) > 0,
          "manual retry did not load board",
        );
        assert.equal(
          client.snapshots,
          snapshots,
          "manual retry required another game snapshot",
        );
        assert.equal(client.requests, 2);
        return {
          requests: client.requests,
          roundOptions: await roundOptions(client.page),
        };
      } finally {
        await client.page.close();
      }
    });
    await check("board_paused_reconnect", async () => {
      const creds = await createRoom("Табло после восстановления");
      let client = await roomPage(creds, false);
      try {
        await joinPlayer(creds, "Первый");
        await joinPlayer(creds, "Второй");
        await client.page
          .getByRole("button", { name: "Начать раунд", exact: true })
          .click();
        await until(
          () => client.state.phase === "running",
          "round did not start",
        );
        await client.page
          .getByRole("button", { name: "Пауза Ⅱ", exact: true })
          .click();
        await until(() => client.state.phase === "paused", "pause missing");
        await client.page.close();
        client = await roomPage(creds, true);
        assert.equal(client.state.phase, "paused");
        const snapshots = client.snapshots;
        await client.page.evaluate(() =>
          window.__themeRegressionSockets
            .find(
              (socket) =>
                socket.url.includes("/api/rooms/") &&
                !socket.url.includes("appearance_only") &&
                socket.readyState === WebSocket.OPEN,
            )
            .close(),
        );
        await until(
          () => client.snapshots > snapshots,
          "no actual reconnect snapshot",
        );
        assert.equal(client.state.phase, "paused");
        await until(
          () => client.requests === 2,
          "same-key paused reconnect did not retry board",
          3000,
        );
        await client.page
          .getByRole("heading", {
            name: "Табло после восстановления",
            exact: true,
          })
          .waitFor();
        return {
          requests: client.requests,
          snapshots: client.snapshots,
          phase: client.state.phase,
        };
      } finally {
        if (!client.page.isClosed()) await client.page.close();
      }
    });
  }
  if (process.env.REGRESSION_CASE !== "board")
    await check("accepted_palette_contrast", paletteContrast);
  assert.deepEqual(pageErrors, [], "unexpected browser page errors");
  assert.equal(failures.length, 0, JSON.stringify(failures));
  console.log(
    JSON.stringify({ ok: true, ...report, pageErrors: pageErrors.length }),
  );
} finally {
  if (registered) {
    const cleanup = await context.request.delete(base + "/api/account/me", {
      data: { password },
    });
    assert.equal(cleanup.status(), 200, "synthetic account cleanup");
  }
  await browser.close();
  await rm(temporary, { recursive: true, force: true });
}
