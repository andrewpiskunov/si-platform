<script lang="ts">
  import { questionTypes, type SessionView } from './game-v2.ts';
  import type { Participant } from './room-client.ts';
  let { view, participants, you }: {view:SessionView;participants:Participant[];you:string} = $props();
  const kind = $derived(view.question?.type || '');
  const stage = $derived(view.stage.kind);
  const host = $derived(participants.some(p=>p.id===you&&p.role==='host'));
  const oral = $derived(view.rules.answer_mode==='oral');
  const active = $derived(view.stage.turn_id || (stage==='select_recipient' ? view.chooser_id : ''));
  const activeName = $derived(participants.find(p=>p.id===active)?.name || 'Участник');
  const participating = $derived(view.players?.some(p=>p.id===you));
  const total = $derived(view.players?.length || 0);
  const accepted = $derived(view.players?.filter(p=>stage==='collect_wagers'?p.wager_submitted:stage==='judge'?p.judged:p.answer_submitted).length || 0);
  const paths:Record<string,string> = {
    stake:'Торги → Ответ победителя → Оценка',
    secret:'Передача вопроса → Ответ получателя → Оценка',
    forAll:'Ответы всех участников → Оценка каждого',
    noRisk:'Ответ без кнопки → Оценка',
    stakeAll:'Закрытые ставки → Ответы → Раскрытие → Оценка'
  };
  const description = $derived.by(() => {
    if(stage==='select_recipient') return view.allowed_actions?.some(a=>a.type==='select_recipient') ? 'Выберите получателя. Он будет отвечать без нажатия кнопки.' : 'Ожидаем выбор получателя вопроса. Он будет отвечать без нажатия кнопки.';
    if(stage==='select_price') return 'Получатель выбирает стоимость вопроса из разрешённого диапазона.';
    if(stage==='auction') return view.auction?.all_in ? 'Объявлен ва-банк. Можно ответить ва-банком или выйти из торгов.' : 'Повышайте ставку по очереди. Пас завершает ваше участие в торгах.';
    if(stage==='collect_wagers') return view.self.wager!==undefined ? 'Ваша ставка принята. Ждём остальных участников.' : !host&&!participating ? 'В этом вопросе участвуют игроки с положительным счётом. Ожидайте их ставки.' : 'Каждый участник делает одну ставку. До раскрытия её видит только он сам.';
    if(stage==='collect_answers'&&!host&&!participating) return 'Ожидаем ответы участников этого вопроса.';
    if(stage==='collect_answers'&&!oral&&host) return 'Идёт сбор ответов. До закрытия видны только отметки готовности.';
    if(stage==='collect_answers') return oral ? host ? 'Выслушайте участников и закройте сбор ответов, чтобы оценить каждого.' : 'Отвечайте голосом. После завершения сбора ведущий оценит каждого участника.' : view.self.answer!==undefined ? 'Ваш ответ принят. Ждём остальных участников.' : 'Запишите ответ. Другие игроки не видят его до раскрытия.';
    if(stage==='collect_answer') return oral ? 'Участник отвечает голосом. Ведущий может сразу оценить ответ.' : 'Участник записывает ответ. Затем ведущий оценивает его.';
    if(stage==='reveal') return 'Ведущий по очереди раскрывает ставку и ответ каждого игрока, затем оценивает ответ.';
    if(stage==='judge') return kind==='forAll' ? host ? 'Оцените каждого участника. После оценки он исчезнет из списка.' : 'Ведущий оценивает каждого участника.' : 'Ведущий оценивает ответ выбранного участника.';
    if(kind==='noRisk') return 'За верный ответ — двойная стоимость. За неверный ответ очки не снимаются.';
    if(kind==='forAll') return 'Отвечают все участники. Нажимать кнопку не нужно.';
    if(kind==='stakeAll') return 'Ответ определит, прибавится ваша ставка к счёту или вычтется из него.';
    return kind==='stake' ? 'Право ответа принадлежит победителю торгов.' : 'Право ответа принадлежит получателю вопроса.';
  });
</script>
{#if paths[kind]}
  <section class="mechanic-guide" data-kind={kind} aria-label="Как проходит вопрос">
    <div class="mechanic-head"><strong>{questionTypes[kind]}</strong><span class="helper">{paths[kind]}</span></div>
    <p>{description}</p>
    <div class="mechanic-status" role="status" aria-live="polite">
      {#if active}<strong>{active===you?'Ваш ход':`Сейчас: ${activeName}`}{host&&active!==you&&['auction','select_recipient','select_price'].includes(stage)?' · можно действовать за участника':''}</strong>{/if}
      {#if stage==='auction' && view.auction}<span>{view.auction.leader_id ? `Ставка ${view.auction.amount} · ${participants.find(p=>p.id===view.auction?.leader_id)?.name || 'Участник'}` : 'Ставок ещё нет'}</span>{/if}
      {#if stage==='collect_wagers'}<span>Ставки приняты: {accepted} из {total}</span>{:else if stage==='collect_answers'&&!oral}<span>Ответы приняты: {accepted} из {total}</span>{:else if stage==='judge'&&kind==='forAll'}<span>Оценены: {accepted} из {total}</span>{/if}
    </div>
  </section>
{/if}
<style>
  .mechanic-guide {padding:12px 16px;border-left:3px solid var(--accent);background:var(--surface);flex:none;}
  .mechanic-head,.mechanic-status {display:flex;justify-content:space-between;gap:8px 16px;flex-wrap:wrap;}
  .mechanic-head strong {color:var(--accent);}
  .mechanic-head .helper {font-size:12px;}
  p {margin:6px 0;font-size:14px;line-height:1.45;}
  .mechanic-status {font-size:12px;color:var(--muted);}
  .mechanic-status:empty {display:none;}
</style>
