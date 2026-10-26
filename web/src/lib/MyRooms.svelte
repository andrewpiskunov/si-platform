<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, errorText } from './api.ts';
  import { user } from './session.ts';
  import { saveRoomCredentials } from './room-credentials.ts';
  import type { Credentials } from './types.ts';
  interface MyRoom {
    room_id: string; title: string; pack_name: string; phase: string; role: string;
    can_return: boolean; players: number; spectators: number; visibility: string;
    created_at: string; expires_at: string;
  }
  let items = $state<MyRoom[]>([]);
  let loading = $state(true), error = $state(''), opening = $state('');
  let offset = $state(0), next = $state(0);
  let disposed = false;
  const phases: Record<string, string> = { lobby: 'Сбор участников', running: 'Идёт игра', open: 'Вопрос открыт', answering: 'Идёт ответ', resolved: 'Между вопросами', paused: 'На паузе', finished: 'Завершена' };
  const roles: Record<string, string> = { host: 'Вы ведущий', player: 'Вы игрок', spectator: 'Вы зритель' };
  onMount(() => { void load(0); return () => { disposed = true; }; });
  async function load(start: number) {
    loading = true; error = '';
    try {
      const result = await api<{ items: MyRoom[]; next_offset?: number }>(`/my-rooms?offset=${start}`, 'GET', undefined, undefined, AbortSignal.timeout(10000));
      if (disposed) return;
      items = result.items; offset = start; next = result.next_offset || 0;
    } catch (e) { if (!disposed) error = errorText(e); }
    finally { if (!disposed) loading = false; }
  }
  async function enter(room: MyRoom) {
    if (opening) return;
    const owner = $user?.id;
    opening = room.room_id; error = '';
    try {
      const credentials = await api<Credentials>(`/my-rooms/${room.room_id}/session`, 'POST', {});
      if (disposed || $user?.id !== owner) return;
      saveRoomCredentials(credentials);
      await goto(`/rooms/${credentials.room_id}${room.phase === "finished" ? "?results=1" : ""}`);
    } catch (e) { if (!disposed) error = errorText(e); }
    finally { if (!disposed) opening = ''; }
  }
</script>

<section id="my-rooms" class="my-rooms stack" aria-labelledby="my-rooms-heading">
  <div class="row between">
    <div><h2 id="my-rooms-heading">Мои комнаты</h2><p class="muted">Созданные вами игры, в том числе по приглашению.</p></div>
    <div class="row"><button disabled={loading || !!opening} onclick={() => load(offset)}>Обновить</button><a class="button primary" href="/rooms?create=1">Создать комнату</a></div>
  </div>
  {#if error}<div class="notice error" role="alert">{error}<button disabled={loading || !!opening} onclick={() => load(offset)}>Обновить список</button></div>{/if}
  {#if loading}<p role="status">Загружаем ваши комнаты…</p>
  {:else if items.length}
    <div class="my-room-grid">
      {#each items as room (room.room_id)}
        <article class="panel stack">
          <div class="row between"><span class="tag">{phases[room.phase] || 'Игра'}</span><span class="helper">{room.visibility === 'public' ? 'Публичная' : 'По приглашению'}</span></div>
          <div><h3>{room.title}</h3><p class="muted">{room.pack_name}</p></div>
          <div class="row"><span class="tag">Создана вами</span><strong>{roles[room.role] || 'Участие завершено'}</strong></div>
          <p class="helper">Игроков: {room.players} · Зрителей: {room.spectators} · Создана {new Date(room.created_at).toLocaleDateString('ru-RU')}</p>
          {#if room.can_return}
            <button class="primary" disabled={!!opening} onclick={() => enter(room)}>{opening === room.room_id ? 'Открываем…' : room.phase === 'finished' ? 'Посмотреть результаты' : room.role === 'host' ? 'Продолжить ведение' : 'Вернуться в комнату'}</button>
            {#if room.role !== 'host'}<p class="helper">Роль ведущего передана другому участнику.</p>{/if}
          {:else}<p class="helper">Ваше место участника удалено. Для возвращения нужно новое приглашение ведущего.</p>{/if}
        </article>
      {/each}
    </div>
  {:else if !error}<div class="panel"><h3>Вы ещё не создали комнату</h3><p class="muted">Создайте игру — здесь появится ссылка для возвращения к ней.</p></div>{/if}
  <div class="row">
    {#if offset > 0}<button disabled={loading || !!opening} onclick={() => load(Math.max(0, offset - 50))}>Предыдущие комнаты</button>{/if}
    {#if next}<button disabled={loading || !!opening} onclick={() => load(next)}>Следующие комнаты</button>{/if}
  </div>
  <p class="helper">Открытие восстанавливает ваше место и текущую роль через аккаунт. Управление переносится в эту вкладку; прежние подключения вашего места закрываются. Комнаты доступны до окончания срока хранения.</p>
</section>

<style>
  .my-rooms { margin-bottom: 40px; scroll-margin-top: 24px; }
  .my-room-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
  h2, h3 { margin: 0; }
  article { overflow-wrap: anywhere; }
  article p { margin: 8px 0 0; }
  article > button { margin-top: auto; }
  @media (max-width: 700px) { .my-room-grid { grid-template-columns: 1fr; } }
</style>
