<script lang="ts">
  import Avatar from './Avatar.svelte';
  import type { RoomState } from './room-client.ts';
  import type { Board } from './types.ts';
  let {
    room,
    board,
    selectedRound,
    blocked,
    canStart,
    canReady,
    onroundchange,
    onstart,
    onready
  }: {
    room: RoomState;
    board: Board | null;
    selectedRound: string;
    blocked: boolean;
    canStart: boolean;
    canReady: boolean;
    onroundchange: (id: string) => void;
    onstart: () => void;
    onready: (ready: boolean) => void;
  } = $props();
  const players = $derived(room.participants.filter((p) => p.role === 'player' && !p.removed));
  const host = $derived(room.participants.find((p) => p.role === 'host' && !p.removed));
  const me = $derived(players.find((p) => p.id === room.you));
  const readyCount = $derived(players.filter((p) => p.ready).length);
  const isHost = $derived(host?.id === room.you);
</script>

<section class="lobby stack" aria-label="Лобби игры">
  <div class="lobby-heading">
    <div>
      <p class="eyebrow">Сегодня играем</p>
      <h2>{board?.pack_name || 'Загружаем пакет…'}</h2>
      <p class="helper">Раундов: {board?.rounds.length || 0} · До 12 игроков</p>
    </div>
    {#if host}<div class="lobby-host">
        <Avatar identity={host.id} />
        <div><span class="helper">Ведущий</span><strong>{host.name}</strong></div>
      </div>{/if}
  </div>
  <div class="row between">
    <h3>Игроки <span class="helper">{players.length} / 12</span></h3>
    <span aria-live="polite">Готовы {readyCount} из {players.length}</span>
  </div>
  <div class="lobby-seats">
    {#each Array(12) as _, i}{@const player = players[i]}
      {#if player}<div class="lobby-seat" class:is-ready={player.ready}>
          <Avatar identity={player.id} />
          <div>
            <strong>{player.name}{player.id === room.you ? ' · вы' : ''}</strong><span
              >{player.ready ? 'Готов к игре' : 'Пока не готов'}</span
            >
          </div>
        </div>{:else}<div class="lobby-seat empty">
          <span class="seat-number">{String(i + 1).padStart(2, '0')}</span>
          <div><strong>Свободное место</strong><span>Ждём игрока</span></div>
        </div>{/if}
    {/each}
  </div>
  <div class="lobby-footer">
    <div>
      <p>
        {players.length < 2
          ? 'Для старта нужны хотя бы два игрока.'
          : room.settings?.require_ready && readyCount < players.length
            ? 'Ждём, пока все игроки подтвердят готовность.'
            : 'Ведущий может начать раунд.'}
      </p>
      <p class="helper">
        Проверьте звук и договоритесь о голосовой связи. Ссылку-приглашение найдёте в панели
        комнаты.
      </p>
    </div>
    {#if canReady && me}<button
        class="primary"
        disabled={blocked}
        onclick={() => onready(!me.ready)}>{me.ready ? 'Снять готовность' : 'Готов к игре'}</button
      >{/if}
    {#if isHost}<div class="lobby-start">
        <label
          >Раунд<select
            value={selectedRound}
            onchange={(event) => onroundchange(event.currentTarget.value)}
            disabled={blocked}
            >{#each board?.rounds || [] as round}<option value={round.id}
                >{round.name}{round.type === 'final' ? ' · финал' : ''}</option
              >{/each}</select
          ></label
        ><button
          class="primary"
          disabled={blocked || !canStart || players.length < 2 || !selectedRound}
          onclick={onstart}>Начать раунд</button
        >
      </div>{/if}
  </div>
</section>

<style>
  .lobby {
    padding: clamp(16px, 3vw, 32px);
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    margin-block: 16px;
  }
  .lobby-heading {
    display: flex;
    justify-content: space-between;
    gap: 24px;
    align-items: center;
  }
  h2 {
    margin: 6px 0;
    font-size: clamp(24px, 3vw, 36px);
    overflow-wrap: anywhere;
  }
  h3 {
    margin: 16px 0;
  }
  .lobby-host {
    display: flex;
    gap: 12px;
    align-items: center;
    max-width: 40%;
  }
  .lobby-host strong,
  .lobby-host span {
    display: block;
    overflow-wrap: anywhere;
  }
  .lobby-seats {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 10px;
  }
  .lobby-seat {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
    padding: 14px;
    border: 1px solid var(--line);
    border-radius: 8px;
  }
  .lobby-seat strong {
    display: block;
    overflow-wrap: anywhere;
  }
  .lobby-seat span {
    font-size: 13px;
    color: var(--muted);
  }
  .lobby-seat.is-ready {
    border-color: var(--accent);
  }
  .lobby-seat.is-ready span {
    color: var(--accent);
  }
  .empty {
    border-style: dashed;
  }
  .seat-number {
    min-width: 40px;
    text-align: center;
    font-variant-numeric: tabular-nums;
    font-size: 22px !important;
  }
  .lobby-footer {
    display: flex;
    gap: 24px;
    justify-content: space-between;
    align-items: center;
    border-top: 1px solid var(--line);
    padding-top: 16px;
  }
  .lobby-footer p {
    margin: 4px 0;
  }
  .lobby-start {
    display: flex;
    gap: 12px;
    align-items: end;
    flex-shrink: 0;
  }
  @media (max-width: 1000px) {
    .lobby-seats {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .lobby-footer {
      flex-wrap: wrap;
    }
  }
  @media (max-width: 540px) {
    .lobby-heading {
      align-items: start;
      flex-direction: column;
    }
    .lobby-host {
      max-width: 100%;
    }
    .lobby-seats {
      grid-template-columns: 1fr;
    }
    .lobby-start {
      flex-wrap: wrap;
      width: 100%;
    }
  }
</style>
