<script lang="ts">
  import { page } from '$app/state';
  import { invalidateAll } from '$app/navigation';
  import { roomContext, contextLink } from '#lib/appearance.ts';
  let { data } = $props();
  let refreshing = $state(false),
    refreshError = $state('');
  const context = $derived(roomContext(page.url));
  const phases: Record<string, string> = {
    lobby: 'Собираемся',
    running: 'Идёт игра',
    open: 'Вопрос открыт',
    answering: 'Идёт ответ',
    resolved: 'Между вопросами',
    paused: 'Пауза'
  };
  function nextLink() {
    const query = new URLSearchParams({ after: data.next, limit: String(data.limit) });
    return contextLink(`/rooms?${query}`, context);
  }
  async function refresh() {
    refreshing = true;
    refreshError = '';
    try {
      await invalidateAll();
    } catch {
      refreshError = 'Не удалось обновить список. Попробуйте ещё раз.';
    } finally {
      refreshing = false;
    }
  }
</script>

<svelte:head><title>Публичные комнаты — Своя игра</title></svelte:head>
<section class="page">
  <div class="row between section-head">
    <div>
      <p class="eyebrow">Есть свободное место</p>
      <h1>Публичные комнаты</h1>
      <p class="muted">
        Выберите игру и представьтесь. Для входа игроком или зрителем аккаунт не нужен.
      </p>
    </div>
    <button disabled={refreshing} onclick={refresh}
      >{refreshing ? 'Обновляем…' : 'Обновить список комнат'}</button
    >
  </div>
  {#if data.error || refreshError}<div class="notice error" role="alert">
      <p>
        {refreshError ||
          (data.error === 'invalid'
            ? 'Некорректный адрес страницы списка.'
            : 'Не удалось загрузить комнаты. Попробуйте ещё раз.')}
      </p>
      {#if data.error === 'invalid'}<a class="button" href={contextLink('/rooms', context)}
          >К началу списка</a
        >
      {:else}<button disabled={refreshing} onclick={refresh}>Повторить загрузку комнат</button>{/if}
    </div>{:else}
    <div class="room-cards">
      {#each data.items as room (room.room_id)}<article class="panel stack public-room">
          <div>
            <p class="eyebrow">{phases[room.phase] || 'Игра'}</p>
            <h2>{room.title || 'Игровая комната'}</h2>
          </div>
          <p class="helper">
            Игроки: {room.players}/{room.player_limit} · Зрители: {room.spectators}/{room.spectator_limit}
          </p>
          <div class="row">
            {#if room.can_join_player}<a
                class="button primary"
                href={`/rooms/${room.room_id}?public=1&role=player`}>Войти игроком</a
              >{:else}<span class="helper">Вход игроком сейчас закрыт</span>{/if}
            {#if room.can_join_spectator}<a
                class="button"
                href={`/rooms/${room.room_id}?public=1&role=spectator`}>Войти зрителем</a
              >{:else}<span class="helper">Нет мест для зрителей</span>{/if}
          </div>
        </article>{:else}<div class="empty">
          <h2>Пока нет публичных комнат</h2>
          <p>Обновите список позже или создайте свою игру из каталога пакетов.</p>
          <a class="button" href={contextLink('/', context)}>Выбрать пакет</a>
        </div>{/each}
    </div>
    <p class="helper" style="margin-top:24px">
      Места и доступ проверяются при входе. Комната может закрыться или заполниться после загрузки
      списка.
    </p>
    <div class="row">
      {#if data.after}<a class="button" href={contextLink('/rooms', context)}>К началу списка</a
        >{/if}
      {#if data.next}<a class="button" href={nextLink()}>Следующие комнаты →</a>{/if}
    </div>
  {/if}
</section>

<style>
  .room-cards {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 20px;
  }
  .public-room h2 {
    overflow-wrap: anywhere;
    margin-bottom: 0;
  }
  .public-room p {
    margin-bottom: 0;
  }
  @media (max-width: 700px) {
    .room-cards {
      grid-template-columns: 1fr;
    }
  }
</style>
