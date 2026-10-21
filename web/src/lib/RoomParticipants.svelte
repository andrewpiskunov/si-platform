<script lang="ts">
  import { api, errorText } from './api.ts';
  import { prepareAction, type SessionView, type V2Command } from './game-v2.ts';
  import type { Participant, RoomState } from './room-client.ts';
  import { removalTargets } from './room-participants.ts';
  let {
    room,
    view,
    token,
    disabled,
    onsend,
    onunauthorized
  }: {
    room: RoomState;
    view: SessionView;
    token: string;
    disabled: boolean;
    onsend: (command: V2Command) => void;
    onunauthorized: () => void;
  } = $props();
  let target = $state(''),
    confirmation = $state<{ id: string; mode: 'kick' | 'ban_guest_session'; seq: number } | null>(
      null
    );
  let roster = $state<Participant[]>([]),
    next = $state<string | undefined>(),
    loading = $state(false),
    loaded = $state(false),
    error = $state('');
  const targets = $derived(removalTargets(room.participants, room.you, view));
  const selected = $derived(targets.find((p) => p.id === target));
  function confirm(mode: 'kick' | 'ban_guest_session') {
    if (selected && !disabled) confirmation = { id: selected.id, mode, seq: room.seq };
  }
  function remove() {
    if (!confirmation || disabled) return;
    error = '';
    try {
      if (confirmation.seq !== room.seq) throw new Error('stale_state');
      onsend(
        prepareAction(view, room.seq, 'remove_participant', {
          target_id: confirmation.id,
          mode: confirmation.mode
        })
      );
      confirmation = null;
    } catch (e) {
      error = errorText(e);
      confirmation = null;
    }
  }
  async function load(after = '') {
    loading = true;
    error = '';
    try {
      const result = await api<{ items: Participant[]; next_after?: string }>(
        `/rooms/${room.room_id}/roster?limit=50${after ? `&after=${encodeURIComponent(after)}` : ''}`,
        'GET',
        undefined,
        token
      );
      roster = after ? [...roster, ...result.items] : result.items;
      next = result.next_after;
      loaded = true;
    } catch (e) {
      if (e instanceof Error && e.message === 'unauthorized') onunauthorized();
      else error = errorText(e);
    } finally {
      loading = false;
    }
  }
</script>

<section class="stack" aria-label="Участники комнаты">
  {#if targets.length}
    <details>
      <summary>Управление участниками</summary>
      <div class="stack" style="margin-top:16px">
        <label
          >Участник для удаления<select
            bind:value={target}
            onchange={() => (confirmation = null)}
            {disabled}
          >
            <option value="">Выберите участника</option>
            {#each targets as participant}<option value={participant.id}
                >{participant.name} · {participant.role === 'player' ? 'игрок' : 'зритель'}</option
              >{/each}
          </select></label
        >
        <button disabled={disabled || !selected} onclick={() => confirm('kick')}
          >Удалить участника</button
        >
        <button
          class="danger"
          disabled={disabled || !selected}
          onclick={() => confirm('ban_guest_session')}>Запретить вход этой сессии</button
        >
        <p class="helper">
          Удаление освобождает место. Участник сможет явно войти заново с нулевым счётом. Запрет
          удаляет все места этой браузерной сессии только в этой комнате. Очистка cookie или другой
          браузер позволяют обойти запрет: он не определяет человека.
        </p>
        {#if confirmation}
          <div class="notice stack" role="group" aria-label="Подтверждение удаления">
            <p>
              {confirmation.mode === 'kick'
                ? 'Удалить участника'
                : 'Запретить вход браузерной сессии участника'} «{room.participants.find(
                (p) => p.id === confirmation?.id
              )?.name}»? Принятые ответы, ставки и история сохранятся. Удаление игрока во время
              вопроса поставит игру на паузу.
            </p>
            <button class="danger" {disabled} onclick={remove}
              >{confirmation.mode === 'kick'
                ? 'Подтвердить удаление'
                : 'Подтвердить запрет сессии'}</button
            >
            <button onclick={() => (confirmation = null)}>Отмена</button>
          </div>
        {/if}
      </div>
    </details>
  {/if}
  <details>
    <summary>История состава</summary>
    <div class="stack" style="margin-top:16px">
      <button disabled={loading} onclick={() => load()}
        >{loaded ? 'Обновить состав' : 'Загрузить состав'}</button
      >
      {#if loaded}
        {#each [false, true] as removed}
          <div>
            <h3>{removed ? 'Удалённые участники' : 'Действующие участники'}</h3>
            {#each roster.filter((p) => !!p.removed === removed) as participant (participant.id)}
              <div class="score-item">
                <span
                  >{participant.name} · {participant.role === 'host'
                    ? 'ведущий'
                    : participant.role === 'player'
                      ? 'игрок'
                      : 'зритель'}{removed ? ' · удалён' : ''}</span
                ><strong>{participant.score}</strong>
              </div>
            {:else}<p class="helper">На загруженных страницах нет.</p>{/each}
          </div>
        {/each}
      {/if}
      {#if next}<button disabled={loading} onclick={() => load(next)}>Ещё участники</button>{/if}
      {#if loading}<p role="status">Загружаем состав…</p>{/if}
    </div>
  </details>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
</section>
