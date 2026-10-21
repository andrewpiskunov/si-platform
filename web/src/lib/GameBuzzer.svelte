<script lang="ts">
  import { onMount } from 'svelte';
  import { actionFor, type SessionView } from './game-v2.ts';
  let {
    view,
    you,
    connected,
    pending,
    paused,
    onsend
  }: {
    view: SessionView;
    you: string;
    connected: boolean;
    pending: number;
    paused: boolean;
    onsend: () => void;
  } = $props();
  let elapsed = $state(0),
    anchor = 0;
  $effect(() => {
    void view.server_time_ms;
    void paused;
    anchor = performance.now();
    elapsed = 0;
  });
  onMount(() => {
    const timer = setInterval(() => (elapsed = performance.now() - anchor), 100);
    return () => clearInterval(timer);
  });
  const self = $derived(view.players?.find((p) => p.id === you));
  const remaining = $derived(
    Math.max(0, (self?.blocked_until_ms || 0) - view.server_time_ms - (paused ? 0 : elapsed))
  );
  const canBuzz = $derived(!!actionFor(view, 'buzz'));
  const reading = $derived(view.stage.kind === 'present_question');
  const label = $derived(
    !connected
      ? 'Восстанавливаем соединение…'
      : paused
        ? 'Игра на паузе'
        : remaining > 0
          ? `Фальстарт · ещё ${(remaining / 1000).toFixed(1)} с`
          : pending
            ? 'Нажатие отправляется…'
            : view.stage.turn_id === you && view.stage.kind === 'collect_answer'
              ? 'Вы отвечаете'
              : view.stage.kind === 'collect_answer'
                ? 'Отвечает другой игрок'
                : self?.attempted
                  ? 'Попытка использована'
                  : reading
                    ? 'Ждите сигнала'
                    : canBuzz
                      ? 'Знаю ответ!'
                      : 'Ожидаем следующий этап'
  );
</script>

<div class="buzzer-dock">
  <button class="buzz" disabled={!connected || paused || pending > 0 || !canBuzz} onclick={onsend}
    >{label}</button
  >
  <p class="helper">
    {reading && canBuzz
      ? 'Раннее нажатие или пробел вызовет фальстарт.'
      : canBuzz
        ? 'Можно нажать кнопку или пробел.'
        : remaining > 0
          ? 'Дождитесь окончания блокировки. Право ответа подтверждает сервер.'
          : 'Состояние кнопки обновляется для всех участников.'}
  </p>
</div>

<style>
  .buzzer-dock {
    margin-top: 16px;
  }
  .buzzer-dock .buzz {
    width: 100%;
    min-height: 58px;
  }
  .buzzer-dock p {
    text-align: center;
    margin: 8px 0;
  }
  @media (max-width: 700px) {
    .buzzer-dock {
      position: sticky;
      bottom: 0;
      background: var(--bg);
      padding: 8px 0;
      z-index: 2;
    }
  }
</style>
