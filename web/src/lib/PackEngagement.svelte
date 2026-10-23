<script lang="ts">
  import type { Card, Engagement, PackageVote } from './types.ts';
  import { ratingText, separateSourceRating, countWord } from './engagement.ts';
  import { api, errorText } from './api.ts';
  import { user, sessionEpoch } from './session.ts';
  let {
    packId,
    initial,
    compact = false
  }: { packId: string; initial: Engagement; compact?: boolean } = $props();
  let stats = $state<Engagement | null>(null);
  let vote = $state<PackageVote>({ rating: null, liked: false });
  let ready = $state(false),
    busy = $state(false),
    error = $state('');
  let generation = 0;
  const e = $derived(stats || initial);
  $effect(() => {
    initial;
    packId;
    stats = null;
  });
  $effect(() => {
    const uid = $user?.id,
      epoch = $sessionEpoch,
      id = packId,
      isCompact = compact;
    const current = ++generation;
    void epoch;
    ready = false;
    busy = false;
    error = '';
    vote = { rating: null, liked: false };
    if (uid && !isCompact) {
      api<PackageVote>(`/catalogue/${id}/vote`)
        .then((v) => {
          if (current === generation) {
            vote = v;
            ready = true;
          }
        })
        .catch((err) => {
          if (current === generation) error = errorText(err);
        });
    }
    return () => {
      ++generation;
    };
  });
  async function save(next: PackageVote) {
    if (!ready || busy) return;
    const current = generation,
      id = packId;
    busy = true;
    error = '';
    try {
      const saved = await api<PackageVote>(`/catalogue/${id}/vote`, 'PUT', next);
      if (current !== generation) return;
      vote = saved;
      const card = await api<Card>(`/catalogue/${id}`);
      if (current === generation && card.engagement) stats = card.engagement;
    } catch (err) {
      if (current === generation) error = errorText(err);
    } finally {
      if (current === generation) busy = false;
    }
  }
</script>

<div class:compact class="engagement" aria-label="Популярность пакета">
  <div class="metrics">
    <div>
      <strong>↓ {e.downloads.toLocaleString('ru-RU')}</strong><span
        >{countWord(e.downloads,['скачивание','скачивания','скачиваний'])}{e.source_downloads == null ? ' у нас' : ''}</span
      >
    </div>
    <div>
      <strong
        >★ {e.rating == null
          ? '—'
          : e.rating.toLocaleString('ru-RU', {
              minimumFractionDigits: 1,
              maximumFractionDigits: 1
            })}</strong
      ><span
        >{e.rating == null
          ? 'нет оценок'
          : `${e.rating_count.toLocaleString('ru-RU')} ${countWord(e.rating_count,['оценка','оценки','оценок'])}`}{separateSourceRating(e)
          ? ' у нас'
          : ''}</span
      >
    </div>
    <div>
      <strong>♥ {e.likes.toLocaleString('ru-RU')}</strong><span
        >{countWord(e.likes,['лайк','лайка','лайков'])}{e.source_likes == null ? ' у нас' : ''}</span
      >
    </div>
  </div>
  {#if separateSourceRating(e)}<p class="source-rating">
      Рейтинг SIBrowser: <strong>{e.source_rating?.toLocaleString('ru-RU')} / 5</strong> · {e.source_rating_count ==
      null
        ? 'число оценок неизвестно'
        : `${e.source_rating_count} оценок`}
    </p>{/if}
  {#if !compact}
    {#if e.source_downloads != null || e.source_likes != null || e.source_rating != null}
      <details class="breakdown">
        <summary>Показатели SIBrowser и нашего сайта</summary>
        <p>
          Скачивания: {e.source_downloads == null
            ? 'источник не указал'
            : e.source_downloads.toLocaleString('ru-RU') + ' на SIBrowser'} · {e.local_downloads.toLocaleString(
            'ru-RU'
          )} у нас.
        </p>
        <p>
          Лайки: {e.source_likes == null
            ? 'источник не указал'
            : e.source_likes.toLocaleString('ru-RU') + ' на SIBrowser'} · {e.local_likes.toLocaleString(
            'ru-RU'
          )} у нас.
        </p>
        <p>Рейтинг у нас: {ratingText(e.local_rating, e.local_rating_count)}.</p>
        {#if e.source_rating != null}<p>
            Рейтинг SIBrowser: {e.source_rating.toLocaleString('ru-RU')} / 5{#if e.source_rating_count != null}
              · {e.source_rating_count.toLocaleString('ru-RU')} оценок{/if}.
          </p>{/if}
      </details>
    {/if}
    {#if $user}
      <div class="vote-controls">
        <fieldset disabled={!ready || busy}>
          <legend>Ваша оценка</legend>
          <div class="stars">
            {#each [1, 2, 3, 4, 5] as n}<button
                type="button"
                class:chosen={vote.rating != null && n <= vote.rating}
                aria-label={`Оценить на ${n} из 5`}
                aria-pressed={vote.rating === n}
                onclick={() => save({ ...vote, rating: n })}
                >★<span class="rating-number">{n}</span></button
              >{/each}
          </div>
        </fieldset>
        <button
          type="button"
          class="like-button"
          class:liked={vote.liked}
          aria-pressed={vote.liked}
          disabled={!ready || busy}
          onclick={() => save({ ...vote, liked: !vote.liked })}
          >♥ {vote.liked ? 'Нравится' : 'Мне нравится'}</button
        >
        {#if vote.rating != null}<button
            type="button"
            class="clear-rating"
            disabled={!ready || busy}
            onclick={() => save({ ...vote, rating: null })}>Убрать оценку</button
          >{/if}
      </div>
      {#if busy}<p class="helper" role="status">Сохраняем…</p>{/if}
    {:else}<p class="helper">Войдите в аккаунт, чтобы оценить пакет и поставить лайк.</p>{/if}
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {/if}
</div>

<style>
  .engagement {
    padding: 22px 0;
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
    margin: 16px 0 24px;
  }
  .metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px;
  }
  .metrics > div {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .metrics strong {
    font-size: 1.45rem;
    font-variant-numeric: tabular-nums;
  }
  .metrics span {
    font-size: 0.78rem;
    color: var(--muted);
  }
  .source-rating,
  .breakdown {
    font-size: 0.82rem;
    color: var(--muted);
    line-height: 1.6;
  }
  .breakdown {
    margin-top: 16px;
  }
  .breakdown p {
    margin: 8px 0;
  }
  .vote-controls {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 16px;
    margin-top: 20px;
  }
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
    min-width: 0;
  }
  legend {
    font-size: 0.8rem;
    color: var(--muted);
    margin-bottom: 8px;
  }
  .stars {
    display: flex;
    gap: 4px;
  }
  .stars button {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    min-width: 36px;
    padding: 6px;
    background: transparent;
    color: var(--muted);
    border: 1px solid var(--line);
    font-size: 1.25rem;
  }
  .stars button.chosen {
    color: var(--accent);
    border-color: var(--accent);
  }
  .rating-number {
    font-size: 0.65rem;
  }
  .like-button {
    background: transparent;
    color: var(--ink);
    border: 1px solid var(--line);
  }
  .like-button.liked {
    color: var(--accent);
    border-color: var(--accent);
  }
  .clear-rating {
    border:0;
    background: transparent;
    color: var(--muted);
    padding: 10px 0;
    font-size: 0.8rem;
    text-decoration: underline;
  }
  .compact {
    margin: 14px 0 0;
    padding: 12px 0 0;
    border-bottom: 0;
  }
  .compact .metrics {
    gap: 8px;
  }
  .compact .metrics strong {
    font-size: 1rem;
  }
  .compact .metrics span {
    font-size: 0.68rem;
  }
  .compact .source-rating {
    font-size: 0.7rem;
    margin-bottom: 0;
  }
  @media (max-width: 420px) {
    .vote-controls {
      gap: 12px;
    }
    .metrics strong {
      font-size: 1.2rem;
    }
  }
</style>
