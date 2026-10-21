<script lang="ts">
  import { classicRules, type Rules } from './game-v2.ts';
  let {
    rules,
    onchange,
    disabled = false
  }: { rules: Rules; onchange: (rules: Rules) => void; disabled?: boolean } = $props();
  function preset(value: string) {
    const next = classicRules();
    if (value === 'friends') {
      next.preset = 'friends';
      next.penalties = false;
      next.false_start = false;
    }
    if (value === 'custom') onchange({ ...rules, preset: 'custom' });
    else onchange(next);
  }
  function change(patch: Partial<Rules>) {
    onchange({ ...rules, ...patch, preset: 'custom', no_risk_multiplier: 2 });
  }
  const timers: [keyof Rules, string][] = [
    ['selection_seconds', 'Выбор вопроса'],
    ['answer_seconds', 'Ответ после кнопки'],
    ['special_seconds', 'Спецвопрос'],
    ['wager_seconds', 'Ставка'],
    ['final_seconds', 'Финал']
  ];
</script>

<fieldset {disabled} class="stack">
  <legend>Правила игры</legend><label
    >Пресет<select value={rules.preset} onchange={(event) => preset(event.currentTarget.value)}
      ><option value="classic">Классика — штрафы и фальстарты</option><option value="friends"
        >Друзья — без штрафов и фальстартов</option
      ><option value="custom">Свои настройки</option></select
    ></label
  >
  <label
    >Открытие кнопки<select
      value={rules.auto_open ? 'auto' : 'manual'}
      onchange={(event) =>
        change({
          auto_open: event.currentTarget.value === 'auto',
          reading_seconds: rules.reading_seconds || 10
        })}
      ><option value="auto">Автоматически после показа вопроса</option><option value="manual"
        >Вручную ведущим</option
      ></select
    ></label
  >
  {#if rules.auto_open}<label
      >Время чтения фрагмента, секунд<input
        type="number"
        min="1"
        max="120"
        required
        value={rules.reading_seconds || 10}
        oninput={(event) => change({ reading_seconds: Number(event.currentTarget.value) })}
      /></label
    >
    <p class="helper">
      После последнего фрагмента откроется кнопка. Длительность из пакета имеет приоритет; медиа без
      указанной длительности и фрагменты с ожиданием ведущего продолжаются вручную.
    </p>{/if}
  <div class="form-grid">
    {#each timers as [key, label]}<label
        >{label}, секунд<input
          type="number"
          min="5"
          max="120"
          required
          value={Number(rules[key])}
          oninput={(event) => change({ [key]: Number(event.currentTarget.value) })}
        /></label
      >{/each}<label
      >Кнопка<select
        value={rules.buzzer_mode}
        onchange={(event) =>
          change({ buzzer_mode: event.currentTarget.value as Rules['buzzer_mode'] })}
        ><option value="first">Первое принятое нажатие</option><option value="random"
          >Случайный выбор в окне</option
        ></select
      ></label
    >{#if rules.buzzer_mode === 'random'}<label
        >Окно нажатия, мс<input
          type="number"
          min="100"
          max="300"
          value={rules.window_ms}
          oninput={(event) => change({ window_ms: Number(event.currentTarget.value) })}
        /></label
      >{/if}{#if rules.false_start}<label
        >Блокировка фальстарта, секунд<input
          type="number"
          min="1"
          max="5"
          value={rules.false_start_seconds}
          oninput={(event) => change({ false_start_seconds: Number(event.currentTarget.value) })}
        /></label
      >{/if}
  </div>
  <label class="check"
    ><input
      type="checkbox"
      checked={rules.penalties}
      onchange={(event) => change({ penalties: event.currentTarget.checked })}
    />Штрафовать за неверный ответ</label
  ><label class="check"
    ><input
      type="checkbox"
      checked={rules.false_start}
      onchange={(event) => change({ false_start: event.currentTarget.checked })}
    />Блокировать раннее нажатие</label
  >
  <p class="helper">
    «Без риска»: двойная стоимость за верный ответ, без штрафа за ошибку. Новые правила применяются
    к следующим вопросам.
  </p>
</fieldset>
