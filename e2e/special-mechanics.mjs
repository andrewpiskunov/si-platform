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
  answer_mode: process.env.ANSWER_MODE || "oral",
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
async function single(host, players, target, correct, delta) {
  await stage(host,'present_question');
  const before=host.state.participants.find(p=>p.id===target.credentials.participant_id).score;
  await button(host,'Продолжить →');
  await stage(host,'collect_answer');
  assert.equal(state(host).stage.turn_id,target.credentials.participant_id);
  if(rules.answer_mode==='written') {
    await form(target,'Ваш ответ').getByLabel('Текст ответа',{exact:true}).fill('Тестовый ответ');
    await button(target,'Отправить ответ');
    await stage(host,'judge');
  } else {
    for(const player of players) assert.equal(await player.page.getByLabel('Текст ответа',{exact:true}).count(),0);
  }
  await button(host,correct?'✓ Верно':'× Неверно');
  await stage(host,'resolved');
  await score(host,target,before+delta);
}
async function collect(host,players) {
  await stage(host,'present_question');
  await button(host,'Продолжить →');
  await stage(host,'collect_answers');
  if(rules.answer_mode==='oral') {
    for(const player of players) assert.equal(await player.page.getByLabel('Текст ответа',{exact:true}).count(),0);
    await button(host,'Закрыть сбор ответов');
  } else {
    for(let i=0;i<players.length;i++) {
      const player=players[i];
      await form(player,'Ваш ответ').getByLabel('Текст ответа',{exact:true}).fill('Закрытый ответ '+i);
      await button(player,'Отправить ответ');
      await until(()=>state(host).players.some(p=>p.id===player.credentials.participant_id&&p.answer_submitted),'answer flag');
      if(i===0) {
        assert.equal(state(host).submissions.find(p=>p.id===player.credentials.participant_id).answer,undefined);
        assert.equal(state(players[1]).submissions.find(p=>p.id===player.credentials.participant_id)?.answer,undefined);
      }
    }
  }
}
try {
  ownerContext=await browser.newContext();contexts.push(ownerContext);
  await request(ownerContext,'POST','/account/register',{username:'special_'+suffix,password,display_name:'Проверка механик'});registered=true;
  const types=['simple','stake','secret','forAll','noRisk','noRiskWrong','stakeAll'];
  const themes=types.map(type=>({id:randomUUID(),name:type,info:{},questions:[question(type==='noRiskWrong'?'noRisk':type,100,'Вопрос '+type,'Ответ '+type)]}));
  themes[2].questions[0].params.push({name:'price',type:'numberSet',number_set:{minimum:100,maximum:300,step:100}});
  const draft=await request(ownerContext,'POST','/drafts',{document:{id:randomUUID(),name:'Все механики '+suffix,version:'5.0',info:{},rounds:[{id:randomUUID(),name:'Все механики',type:'standard',info:{},themes}]}});
  assert.deepEqual((draft.play_issues||[]).filter(i=>i.severity==='error'),[]);
  const credentials=await request(ownerContext,'POST','/rooms',{host_name:'Ведущий',pack_id:draft.card.id,revision:draft.card.revision,rules});
  const host=await newClient(credentials,'Ведущий',ownerContext);
  const players=[];
  for(const name of ['Игрок Альфа','Игрок Бета']) {
    const joined=await request(ownerContext,'POST',`/rooms/${credentials.room_id}/participants`,{name,role:'player',invite_token:credentials.invite_token});
    players.push(await newClient(joined,name));
  }
  await until(()=>host.state.participants.length===3,'joined');
  await adjust(host,players[0],1000);await adjust(host,players[1],1000);
  await button(host,'Начать раунд');
  checkpoint='ordinary';
  await button(host,'simple, 100 очков');await stage(host,'present_question');await button(host,'Продолжить →');await stage(players[0],'buzz_open');await button(players[0],'Знаю ответ!');await stage(host,'collect_answer');
  if(rules.answer_mode==='written'){await form(players[0],'Ваш ответ').getByLabel('Текст ответа',{exact:true}).fill('Ответ');await button(players[0],'Отправить ответ');await stage(host,'judge');}
  await button(host,'✓ Верно');await stage(host,'resolved');
  checkpoint='auction';
  await button(host,'stake, 100 очков');await stage(host,'auction');
  await host.page.locator('.mechanic-guide[data-kind="stake"]').waitFor();
  await host.page.screenshot({animations:'disabled',path:output+'/auction-guide-'+rules.answer_mode+'.png'});
  let bidder=players.find(p=>p.credentials.participant_id===state(host).stage.turn_id);
  let controls=form(bidder,'Аукцион');await controls.getByLabel('Сумма',{exact:false}).fill('100');await controls.getByRole('button',{name:'Сделать ставку',exact:true}).click();
  await until(()=>state(host).auction.amount===100,'first bid');
  bidder=players.find(p=>p.credentials.participant_id===state(host).stage.turn_id);
  await stage(bidder,'auction');controls=form(bidder,'Аукцион');await controls.getByLabel('Сумма',{exact:false}).fill('200');await controls.getByRole('button',{name:'Сделать ставку',exact:true}).click();
  await until(()=>state(host).auction.amount===200,'second bid');
  const winner=bidder;
  bidder=players.find(p=>p.credentials.participant_id===state(host).stage.turn_id);await button(bidder,'Пас');
  await single(host,players,winner,true,200);
  checkpoint='secret recipient and price';
  await button(host,'secret, 100 очков');await stage(host,'select_recipient');
  assert.equal(state(players[0]).question.text,'');assert.equal(state(players[1]).question.text,'');
  const recipient=players.find(p=>p.credentials.participant_id!==state(host).chooser_id);
  controls=form(host,'Кому передать вопрос');await controls.getByLabel(/^Участник/).selectOption(recipient.credentials.participant_id);await controls.getByRole('button',{name:'Передать вопрос',exact:true}).click();
  await stage(recipient,'select_price');controls=form(recipient,'Стоимость вопроса');await controls.getByLabel(/^Сумма/).fill('300');await controls.getByRole('button',{name:'Установить стоимость',exact:true}).click();
  await single(host,players,recipient,true,300);
  checkpoint='for all';
  await button(host,'forAll, 100 очков');await collect(host,players);await stage(host,'judge');
  for(let i=0;i<players.length;i++) {
    controls=form(host,'Оценить ответ');await controls.getByLabel(/^Участник/).selectOption(players[i].credentials.participant_id);await controls.getByRole('button',{name:i===0?'✓ Верно':'× Неверно',exact:true}).click();
    await until(()=>state(host).players.find(p=>p.id===players[i].credentials.participant_id).judged,'judgement');
  }
  await stage(host,'resolved');
  for(const kind of ['noRisk','noRiskWrong']) {
    checkpoint=kind;await button(host,kind+', 100 очков');
    const target=players.find(p=>p.credentials.participant_id===state(host).chooser_id);
    await single(host,players,target,kind==='noRisk',kind==='noRisk'?200:0);
  }
  checkpoint='closed wagers';
  await button(host,'stakeAll, 100 очков');await stage(host,'collect_wagers');
  const instance=state(host).question.instance_id;
  const amounts=[73,127];
  for(let i=0;i<players.length;i++) {
    controls=form(players[i],'Ваша закрытая ставка');await controls.getByLabel(/^Сумма/).fill(String(amounts[i]));await controls.getByRole('button',{name:'Зафиксировать ставку',exact:true}).click();
    await until(()=>state(host).players.find(p=>p.id===players[i].credentials.participant_id).wager_submitted,'wager submitted');
    assert.equal(state(host).submissions.find(p=>p.id===players[i].credentials.participant_id).wager,undefined);
  }
  await collect(host,players);await stage(host,'reveal');
  for(let i=0;i<players.length;i++) {
    const target=players.find(p=>p.credentials.participant_id===state(host).stage.turn_id);
    const before=host.state.participants.find(p=>p.id===target.credentials.participant_id).score;
    const amount=amounts[players.indexOf(target)];
    await button(host,'Раскрыть: '+target.name);await stage(host,'judge');await button(host,i===0?'✓ Верно':'× Неверно');
    await score(host,target,before+(i===0?amount:-amount));await stage(host,i===players.length-1?'resolved':'reveal');
  }
  checkPrivateViews(instance,players,[host]);
  assert.deepEqual(pageErrors,[]);
  await host.page.screenshot({animations:'disabled',path:output+'/special-mechanics-'+rules.answer_mode+'.png'});
  console.log(JSON.stringify({mode:rules.answer_mode,ordinary:true,auction:true,recipient_and_price:true,for_all:true,no_risk_reward_and_no_penalty:true,closed_wagers:true,private_answers:true,page_errors:0}));
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
