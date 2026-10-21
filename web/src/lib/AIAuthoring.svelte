<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { contextLink, roomContext } from './appearance.ts';
  import { api, APIError } from './api.ts';
  import { user, sessionEpoch } from './session.ts';
  import { UsageRequestScope } from './usage-model.ts';
  import {
    AIQuoteState,
    aiError,
    activeJob,
    money,
    jobLabel,
    normalizeAIRequest
  } from './ai-model.ts';
  import type { AIRequest, AIResult, AIStatus, AIQuote, AIJob, AIPage } from './ai-model.ts';
  import type { Draft, PackDocument } from './types.ts';
  let {
    rounds = [],
    ontheme
  }: {
    rounds?: { id: string; name: string; type?: string }[];
    ontheme?: (document: PackDocument, round: string) => void;
  } = $props();
  let request = $state<AIRequest>({
    mode: 'pack',
    topic: '',
    audience: 'Взрослые',
    difficulty: 3,
    themes: 1,
    questions_per_theme: 5
  });
  let status = $state<AIStatus | null>(null),
    quote = $state<AIQuote | null>(null),
    jobs = $state<AIJob[]>([]),
    next = $state('');
  let job = $state<AIJob | null>(null),
    result = $state<AIResult | null>(null),
    reviewed = $state(false),
    roundID = $state('');
  let busy = $state(false),
    error = $state(''),
    notice = $state(''),
    mounted = $state(false),
    now = $state(Date.now()),
    retry = $state(false);
  let polls = $state(0);
  const scope = new UsageRequestScope(),
    quoteState = new AIQuoteState();
  let ticket: ReturnType<UsageRequestScope['begin']>;
  const identity = () => `${$user?.id ?? ''}:${$sessionEpoch}`;
  const signature = $derived(JSON.stringify(request));
  const validQuote = $derived(
    quote !== null &&
      JSON.stringify(quote.request) === signature &&
      (retry || now < Date.parse(quote.expires_at))
  );
  const standardRounds = $derived(rounds.filter((r) => (r.type || 'standard') === 'standard'));
  onMount(() => {
    mounted = true;
    const timer = setInterval(() => {
      now = Date.now();
      if (!busy && job && activeJob(job.status) && polls < 100) {
        polls++;
        void refreshJob();
      }
    }, 2000);
    return () => {
      clearInterval(timer);
      scope.dispose();
    };
  });
  $effect(() => {
    const account = identity();
    if (!mounted) return;
    untrack(() => {
      ticket = scope.begin(account);
      request = {
        mode: 'pack',
        topic: '',
        audience: 'Взрослые',
        difficulty: 3,
        themes: 1,
        questions_per_theme: 5
      };
      status = null;
      quote = null;
      jobs = [];
      next = '';
      job = null;
      result = null;
      reviewed = false;
      error = '';
      notice = '';
      busy = false;
      retry = false;
      polls = 0;
      quoteState.clear();
      void refresh();
    });
  });
  async function execute<T>(
    work: (signal: AbortSignal) => Promise<T>,
    apply: (value: T) => void | Promise<void>
  ) {
    if (busy || !ticket) return;
    const current = ticket;
    busy = true;
    error = '';
    try {
      const value = await work(current.signal);
      if (scope.current(current, identity)) await apply(value);
    } catch (e) {
      if (!scope.current(current, identity)) return;
      error = aiError(e);
      if (e instanceof APIError && [401, 403].includes(e.status)) {
        status = null;
        jobs = [];
        job = null;
        result = null;
        quote = null;
        reviewed = false;
        quoteState.clear();
        retry = false;
      }
    } finally {
      if (scope.current(current, identity)) busy = false;
    }
  }
  function refresh() {
    return execute(
      (signal) =>
        Promise.all([
          api<AIStatus>('/ai/status', 'GET', undefined, undefined, signal),
          api<AIPage>('/ai/jobs', 'GET', undefined, undefined, signal)
        ]),
      ([s, p]) => {
        status = s;
        jobs = p.jobs || [];
        next = p.next || '';
      }
    );
  }
  function more() {
    return execute(
      (signal) =>
        api<AIPage>(
          `/ai/jobs?after=${encodeURIComponent(next)}`,
          'GET',
          undefined,
          undefined,
          signal
        ),
      (p) => {
        jobs = [...jobs, ...(p.jobs || []).filter((j) => !jobs.some((old) => old.id === j.id))];
        next = p.next || '';
      }
    );
  }
  function select(value: AIJob) {
    job = value;
    result = value.result ? JSON.parse(JSON.stringify(value.result)) : null;
    reviewed = false;
    notice = '';
    polls = 0;
  }
  function updateJob(value: AIJob) {
    if (job?.id !== value.id || (!result && value.result)) select(value);
    else job = value;
    jobs = [value, ...jobs.filter((j) => j.id !== value.id)];
  }
  function refreshJob() {
    if (!job) return;
    const id = job.id;
    return execute(
      (signal) => api<AIJob>(`/ai/jobs/${id}`, 'GET', undefined, undefined, signal),
      updateJob
    );
  }
  function estimate(event: SubmitEvent) {
    event.preventDefault();
    const input = normalizeAIRequest(JSON.parse(signature) as AIRequest);
    request = input;
    quote = null;
    quoteState.clear();
    retry = false;
    return execute(
      (signal) => api<AIQuote>('/ai/quotes', 'POST', input, undefined, signal),
      (value) => {
        if (JSON.stringify(input) !== JSON.stringify(request)) return;
        quote = value;
        quoteState.set(value.id, JSON.stringify(value.request), Date.parse(value.expires_at));
      }
    );
  }
  function start() {
    if (!validQuote || busy) return;
    if (!quoteState.canStart(signature, Date.now())) {
      quote = null;
      quoteState.clear();
      retry = false;
      error = 'Оценка истекла. Рассчитайте заново.';
      return;
    }
    const body = quoteState.start(signature, Date.now(), () => crypto.randomUUID());
    retry = true;
    return execute(
      (signal) => api<AIJob>('/ai/jobs', 'POST', body, undefined, signal),
      (value) => {
        updateJob(value);
        quote = null;
        quoteState.clear();
        retry = false;
      }
    );
  }
  function cancel() {
    if (!job) return;
    return execute(
      (signal) => api<AIJob>(`/ai/jobs/${job!.id}/cancel`, 'POST', {}, undefined, signal),
      updateJob
    );
  }
  function accept() {
    if (!job || !result || !reviewed || job.status !== 'ready') return;
    const body = { reviewed: true, result: JSON.parse(JSON.stringify(result)) };
    const id = job.id;
    if (job.request.mode === 'theme' && ontheme) {
      const target = roundID;
      return execute(
        (signal) =>
          api<{ document: PackDocument }>(
            `/ai/jobs/${id}/preview`,
            'POST',
            body,
            undefined,
            signal
          ),
        (value) => {
          ontheme?.(value.document, target);
          notice = 'Тема добавлена в рабочую копию пакета.';
          reviewed = false;
        }
      );
    }
    return execute(
      (signal) => api<Draft>(`/ai/jobs/${id}/draft`, 'POST', body, undefined, signal),
      async (draft) => {
        notice = 'Создан новый частный черновик.';
        await goto(contextLink(`/studio/${draft.card.id}`, roomContext(page.url)));
      }
    );
  }
</script>

<details class="ai-panel" data-testid="ai-authoring">
  <summary>ИИ-помощник: предложения для автора</summary>
  <div class="stack">
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}
    {#if notice}<p class="notice success" role="status">{notice}</p>{/if}
    {#if status?.provider === 'fixture'}<p class="notice" data-testid="ai-fixture">
        <strong
          >Детерминированный тестовый провайдер. Это не генерация моделью; реальных платежей и
          запросов к модели нет.</strong
        >
      </p>{/if}
    {#if status && !status.enabled}<p class="notice" data-testid="ai-disabled">
        ИИ-помощник не настроен оператором. Генерация отключена; история заданий доступна.
      </p>{/if}
    {#if status}<p>
        Модель: {status.model || 'не настроена'}. Месячный бюджет: {money(status.monthly_limit)};
        учтено {money(status.spent)}; зарезервировано {money(status.reserved)}; осталось {money(
          status.remaining
        )}.
      </p>{/if}
    <p class="helper">
      Стоимость — внутренняя оценка бюджета по тарифам оператора, а не платёж или счёт. Обновите
      данные, чтобы увидеть текущий остаток.
    </p>
    <button onclick={refresh} disabled={busy}>Обновить ИИ и историю</button>
    <form
      class="stack"
      onsubmit={estimate}
      oninput={() => {
        quote = null;
        quoteState.clear();
        retry = false;
      }}
    >
      <fieldset disabled={busy || !status?.enabled} class="stack">
        <label
          >Что подготовить<select
            bind:value={request.mode}
            onchange={() => {
              request.themes = 1;
            }}
            ><option value="pack">Новый частный пакет</option>{#if ontheme}<option value="theme"
                >Тема для этого пакета</option
              >{/if}</select
          ></label
        >
        <label
          >Тема запроса<textarea bind:value={request.topic} required maxlength="2000"
          ></textarea></label
        >
        <label>Аудитория<input bind:value={request.audience} required maxlength="200" /></label>
        <label
          >Сложность (1–5)<input
            type="number"
            bind:value={request.difficulty}
            min="1"
            max="5"
            required
          /></label
        >
        {#if request.mode === 'pack'}<label
            >Количество тем<input
              type="number"
              bind:value={request.themes}
              min="1"
              max="10"
              required
            /></label
          >{/if}
        <label
          >Вопросов в теме<input
            type="number"
            bind:value={request.questions_per_theme}
            min="1"
            max="10"
            required
          /></label
        >
        <p>
          Уже при расчёте оценки текст запроса и аудитория передаются внешнему провайдеру (в
          тестовом режиме — только локально). Не вводите секреты. Факты и источники не проверены;
          ссылки не открываются автоматически.
        </p>
        <button type="submit">Рассчитать оценку</button>
      </fieldset>
    </form>
    {#if quote}
      <div class="notice" data-testid="ai-quote">
        <p>
          Максимальный расход: {money(quote.maximum_cost)}. Модель: {quote.model}. Вход: {quote.input_tokens}
          токенов; максимум ответа: {quote.max_output_tokens}. Лимит может привести к неполному
          результату.
        </p>
        <p>
          Оценка действует до {new Date(quote.expires_at).toLocaleTimeString('ru-RU')} (5 минут). {#if !validQuote}Запрос
            изменён или оценка истекла — рассчитайте заново.{/if}
        </p>
        <button onclick={start} disabled={busy || !validQuote || !status?.enabled}
          >{retry ? 'Повторить запуск с тем же ключом' : 'Запустить по оценке'}</button
        >
      </div>
    {/if}
    <h3>Мои задания</h3>
    {#if !jobs.length}<p>Нет загруженных заданий.</p>{/if}
    {#each jobs as item (item.id)}<button class="small" disabled={busy} onclick={() => select(item)}
        >{item.request.topic} · {jobLabel[item.status] || item.status}</button
      >{/each}
    {#if next}<button disabled={busy} onclick={more}>Ещё задания (до 50)</button>{/if}
    {#if job}
      <section class="stack" data-testid="ai-job">
        <h3>{jobLabel[job.status] || job.status}</h3>
        <p>Максимум {money(job.maximum_cost)}; фактическая оценка {money(job.actual_cost)}.</p>
        {#if job.status === 'uncertain' || job.cancel_requested}<p class="notice">
            Отмена у провайдера не гарантирована. При неизвестном исходе резерв {money(
              job.maximum_cost
            )} удерживается; повторной генерации автоматически не будет.
          </p>{/if}
        {#if job.error}<p class="notice">
            Результат не получен. Задание автоматически не повторяется.
          </p>{/if}
        <button
          disabled={busy}
          onclick={() => {
            polls = 0;
            void refreshJob();
          }}>Обновить задание</button
        >
        {#if activeJob(job.status)}<button disabled={busy || job.cancel_requested} onclick={cancel}
            >Отменить задание</button
          >{/if}
        {#if polls >= 100 && activeJob(job.status)}<p>
            Автообновление остановлено. Обновите задание вручную.
          </p>{/if}
        {#if result && job.status === 'ready'}
          <p class="notice">
            Предложение ИИ: факты и источники не проверены. Исправьте материал и проверьте источники
            самостоятельно. Принятие не отмечает факты проверенными.
          </p>
          <fieldset
            disabled={busy}
            class="stack"
            oninput={() => {
              reviewed = false;
            }}
          >
            <label>Название предложения<input bind:value={result.title} maxlength="200" /></label>
            {#each result.themes as theme, ti}
              <label>Название темы {ti + 1}<input bind:value={theme.name} maxlength="200" /></label>
              {#each theme.questions as question, qi}
                <div class="panel stack">
                  <label
                    >Вопрос {ti + 1}.{qi + 1}<textarea bind:value={question.text} maxlength="4000"
                    ></textarea></label
                  >
                  <label
                    >Ответ {ti + 1}.{qi + 1}<textarea bind:value={question.answer} maxlength="1000"
                    ></textarea></label
                  >
                  <label
                    >Источники {ti + 1}.{qi + 1} (до 5, по одному на строку)<textarea
                      value={question.sources.join('\n')}
                      oninput={(e) => {
                        question.sources = e.currentTarget.value
                          .split('\n')
                          .filter((s) => s.trim());
                      }}></textarea></label
                  >
                  <label
                    >Комментарий {ti + 1}.{qi + 1}<textarea
                      bind:value={question.comment}
                      maxlength="2000"></textarea></label
                  >
                </div>
              {/each}
            {/each}
          </fieldset>
          {#if job.request.mode === 'theme' && ontheme}<label
              >Обычный раунд<select bind:value={roundID}
                ><option value="">Выберите раунд</option>{#each standardRounds as round}<option
                    value={round.id}>{round.name}</option
                  >{/each}</select
              ></label
            >{/if}
          <label class="check"
            ><input type="checkbox" bind:checked={reviewed} disabled={busy} />Я просмотрел
            предложение и принимаю ответственность за проверку фактов и источников</label
          >
          <button
            class="primary"
            onclick={accept}
            disabled={busy ||
              !reviewed ||
              (job.request.mode === 'theme' &&
                !!ontheme &&
                !standardRounds.some((r) => r.id === roundID))}
            >{job.request.mode === 'theme' && ontheme
              ? 'Добавить тему в пакет'
              : 'Создать частный черновик'}</button
          >
          <button
            onclick={() => {
              result = null;
              reviewed = false;
              notice = 'Предложение скрыто. Его можно снова открыть из истории.';
            }}
            disabled={busy}>Отклонить предложение</button
          >
        {/if}
      </section>
    {/if}
  </div>
</details>

<style>
  .ai-panel {
    margin: 20px 0;
  }
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
    min-width: 0;
  }
</style>
