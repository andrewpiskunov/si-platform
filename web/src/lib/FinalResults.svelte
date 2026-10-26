<script lang="ts">
  import type { RoomState } from './room-client.ts';
  let { room }: { room: RoomState } = $props();
  const ranking = $derived.by(() => {
    const players = room.participants
      .filter((p) => p.role === 'player' && !p.removed)
      .toSorted((a, b) => b.score - a.score);
    return players.map((player) => ({
      ...player,
      rank: players.findIndex((p) => p.score === player.score) + 1,
      tied: players.filter((p) => p.score === player.score).length > 1
    }));
  });
</script>

<section class="final-results" data-testid="final-results" aria-labelledby="final-results-title">
  <p class="eyebrow">Игра завершена</p>
  <h2 id="final-results-title">Итоги игры</h2>
  {#if ranking.length}
    <p class="leaders">
      {ranking.filter((p) => p.rank === 1).length > 1 ? 'Первое место делят' : 'Первое место'}: {ranking
        .filter((p) => p.rank === 1)
        .map((p) => p.name)
        .join(' · ')}
    </p>
    <ol class="ranking" aria-label="Итоговые места">
      {#each ranking as player (player.id)}
        <li
          class:leader={player.rank === 1}
          data-testid="final-ranking-row"
          data-player-id={player.id}
          data-rank={player.rank}
        >
          <span class="place">{player.rank}<small>место</small></span>
          <span class="name"
            >{player.name}{#if player.tied}<small>Делит место</small>{/if}</span
          >
          <strong>{player.score}<small>очков</small></strong>
        </li>
      {/each}
    </ol>
  {:else}<p>Нет игроков для итоговой таблицы.</p>{/if}
  <p class="helper">Итоговый счёт сохранён. При равном счёте игроки занимают одно место.</p>
</section>

<style>
  .final-results {
    padding: clamp(18px, 2vw, 28px);
    border: 2px solid var(--accent);
    border-radius: 16px;
    background: var(--surface);
  }
  h2 {
    font-size: clamp(30px, 4vw, 54px);
    margin: 0 0 8px;
  }
  .leaders {
    font-size: clamp(18px, 2vw, 26px);
    overflow-wrap: anywhere;
    margin: 8px 0 0;
  }
  .ranking {
    display: grid;
    gap: 10px;
    padding: 0;
    list-style: none;
    margin: 16px 0;
  }
  li {
    display: grid;
    grid-template-columns: 60px minmax(0, 1fr) auto;
    align-items: center;
    gap: 16px;
    padding: 12px 16px;
    border: 1px solid var(--line);
    border-radius: 12px;
  }
  li.leader {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 12%, transparent);
  }
  .place,
  strong {
    font-size: clamp(24px, 3vw, 36px);
    font-variant-numeric: tabular-nums;
  }
  .place {
    color: var(--accent);
  }
  small {
    display: block;
    font-size: 12px;
    font-weight: normal;
    color: var(--muted);
  }
  .name {
    font-size: 20px;
    overflow-wrap: anywhere;
  }
  strong {
    text-align: right;
  }
  .eyebrow {
    margin: 0 0 8px;
  }
  .helper {
    margin: 12px 0 0;
  }
  @media (min-width: 701px) and (max-height: 800px) {
    .final-results {
      padding: 16px 22px;
    }
    h2 {
      font-size: 32px;
      line-height: 1.15;
    }
    .leaders {
      font-size: 18px;
      margin-top: 6px;
    }
    .ranking {
      gap: 8px;
      margin: 12px 0;
    }
    li {
      padding: 8px 14px;
    }
    .place,
    strong {
      font-size: 28px;
      line-height: 1.2;
    }
    .name {
      font-size: 18px;
    }
    .helper {
      margin-top: 8px;
    }
  }
  @media (max-width: 500px) {
    li {
      grid-template-columns: 40px minmax(0, 1fr) auto;
      gap: 8px;
      padding: 12px;
    }
    .name {
      font-size: 16px;
    }
  }
</style>
