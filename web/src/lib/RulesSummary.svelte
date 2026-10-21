<script lang="ts">
  import type { Rules } from './game-v2.ts';
  let { rules }: { rules: Rules } = $props();
</script>

<div class="rules-summary">
  <dl>
    <div>
      <dt>Выбор вопроса</dt>
      <dd>{rules.selection_seconds} с</dd>
    </div>
    <div>
      <dt>Ответ после кнопки</dt>
      <dd>{rules.answer_seconds} с</dd>
    </div>
    <div>
      <dt>Спецвопрос</dt>
      <dd>{rules.special_seconds} с</dd>
    </div>
    <div>
      <dt>Ставка</dt>
      <dd>{rules.wager_seconds} с</dd>
    </div>
    <div>
      <dt>Финал</dt>
      <dd>{rules.final_seconds} с</dd>
    </div>
    <div>
      <dt>Кнопка</dt>
      <dd>
        {rules.buzzer_mode === 'random'
          ? 'Случайный выбор в окне'
          : 'Первое принятое сервером нажатие'}
      </dd>
    </div>
    <div>
      <dt>Окно случайного выбора</dt>
      <dd>{rules.window_ms} мс{rules.buzzer_mode === 'first' ? ' · не используется' : ''}</dd>
    </div>
    <div>
      <dt>Фальстарт</dt>
      <dd>
        {rules.false_start ? `Блокировка на ${rules.false_start_seconds} с` : 'Без блокировки'}
      </dd>
    </div>
    <div>
      <dt>Штраф за неверный ответ</dt>
      <dd>{rules.penalties ? 'Включён' : 'Выключен'}</dd>
    </div>
  </dl>
  <p class="helper">В финал проходят игроки с положительным счётом на момент начала финала.</p>
  <p class="helper">
    «Без риска»: двойная стоимость за верный ответ, без штрафа за ошибку. Правила начатого вопроса
    зафиксированы; изменения применяются к следующим вопросам.
  </p>
</div>

<style>
  dl {
    display: grid;
    gap: 10px;
    margin: 16px 0;
  }
  dl > div {
    display: flex;
    gap: 12px;
    justify-content: space-between;
    flex-wrap: wrap;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    font-weight: 600;
  }
  p:last-child {
    margin-bottom: 0;
  }
</style>
