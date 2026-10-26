<script lang="ts">
  import Avatar from './Avatar.svelte';
  import { playerDecision } from './game-feedback.ts';
  import type { SessionView } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  let { room, view }: { room: RoomState; view: SessionView } = $props();
  const players = $derived(room.participants.filter((p) => p.role === 'player' && !p.removed));
  const host = $derived(room.participants.find((p) => p.role === 'host' && !p.removed));
</script>

{#if room.phase !== 'finished'}<section class="participants-strip" aria-label="Счёт игроков">
    {#if room.phase !== 'lobby'}<div>
        <h3>Игроки · {players.length}/12</h3>
        <div class="score-list">
          {#each players as participant}{@const status = view.players?.find(
              (p) => p.id === participant.id
            )}
            <div class="score-item" class:winner={view.stage.turn_id === participant.id}>
              <Avatar identity={participant.id} />
              <div class="participant-name">
                {participant.name}
                <div class="helper">
                  {status?.wager_submitted ? 'Ставка принята · ' : ''}{status?.answer_submitted
                    ? 'Ответ принят'
                    : status?.attempted
                      ? 'Попытка использована'
                      : ''}{status?.judged ? ' · Оценён' : ''}
                </div>
              </div>
              <strong>{participant.score}</strong>
              {#if playerDecision(view, participant.id)}{@const decision = playerDecision(
                  view,
                  participant.id
                )!}{#key decision.id}<small class="score-change"
                    >{decision.correct ? 'Верно' : 'Неверно'} · {decision.delta > 0
                      ? '+'
                      : ''}{decision.delta}</small
                  >{/key}{/if}
            </div>{/each}
        </div>
        {#if host}<div class="host-identity">
            <Avatar identity={host.id} small />
            <div>
              <span class="helper">Ведущий</span>
              <div>{host.name}</div>
            </div>
          </div>{/if}
      </div>
    {/if}
  </section>{/if}

<style>
  .participants-strip {
    margin-top: 20px;
  }
  .participants-strip :global(.score-list) {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 10px;
  }
  .participants-strip :global(.score-item) {
    min-width: 0;
  }
  .score-item .score-change {
    grid-column: 2;
    font-size: 12px;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }
  @media (prefers-reduced-motion: no-preference) {
    .score-change {
      animation: score-change 0.35s ease-out;
    }
    @keyframes score-change {
      from {
        opacity: 0;
        transform: translateY(5px);
      }
      to {
        opacity: 1;
        transform: none;
      }
    }
  }
  @media (min-width: 1300px) {
    .participants-strip :global(.score-list:has(.score-item:nth-child(4))) {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }
  @media (max-width: 700px) {
    .participants-strip :global(.score-list) {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
