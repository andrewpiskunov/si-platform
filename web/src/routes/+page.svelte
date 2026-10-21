<script lang="ts">
  import { page } from '$app/state';
  import { contextLink, roomContext } from '#lib/appearance.ts';
  const room = $derived(roomContext(page.url));
  import PackCard from '#lib/PackCard.svelte';
  let { data } = $props();
</script>

<section class="page">
  <div class="hero">
    <div>
      <p class="eyebrow">Ваши друзья. Ваши вопросы. Ваша игра.</p>
      <h1>Хороший вечер<br />начинается <span>с вопроса.</span></h1>
      <p>Выберите пакет, пригласите друзей и выясните, кто сегодня знает чуть больше.</p>
      <div class="row">
        <a class="button primary" href="#catalogue"
          >Выбрать пакет <span aria-hidden="true">↓</span></a
        ><a class="button ghost" href={contextLink('/studio', room)}>Создать свой</a>
      </div>
    </div>
    <div class="hero-board" aria-hidden="true">
      <div class="hero-board-top"><span>СВОЯ ИГРА</span><span>ВЕЧЕР С ДРУЗЬЯМИ</span></div>
      <div class="hero-board-cells">
        {#each [100, 100, 100, 200, 200, 200, 300, 300, 300] as value}<span>{value}</span>{/each}
      </div>
      <div class="hero-board-caption">Выбирайте тему. Жмите кнопку. Забирайте очки.</div>
    </div>
  </div>
  <div id="catalogue" class="section-head row between">
    <div>
      <p class="eyebrow">Есть о чём подумать</p>
      <h2>Каталог пакетов</h2>
      <p>Готовые вопросы для вашей следующей игры</p>
    </div>
    <a href={contextLink('/studio', room)} class="text-link">Авторская мастерская ↗</a>
  </div>
  <form class="filters" action="/" method="GET">
    {#if room}<input type="hidden" name="room" value={room} />{/if}
    <input
      class="search"
      name="q"
      aria-label="Поиск пакетов"
      value={data.filters.q || ''}
      placeholder="Название, автор или тема…"
    /><select name="language" aria-label="Язык" value={data.filters.language || ''}
      ><option value="">Все языки</option><option value="ru">Русский</option><option value="en"
        >Английский</option
      ></select
    ><select name="difficulty" aria-label="Сложность" value={data.filters.difficulty || ''}
      ><option value="">Любая сложность</option><option value="0">Не указана</option
      >{#each [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as level}<option value={level}>{level} из 10</option
        >{/each}</select
    ><select name="media" aria-label="Медиа" value={data.filters.media || ''}
      ><option value="">Любой формат</option><option value="true">С медиа</option><option
        value="false">Без медиа</option
      ></select
    ><button type="submit">Найти</button>
    <details class="full">
      <summary>Ещё фильтры</summary>
      <div class="form-grid">
        <label
          >Возрастная отметка
          <select
            name="age"
            aria-label="Возрастная отметка"
            aria-describedby="age-filter-help"
            value={data.filters.age || ''}
          >
            <option value="">Все отметки</option>
            {#each [0, 6, 12, 16, 18] as age}<option value={String(age)}>Не выше {age}+</option
              >{/each}
            <option value="unrated">Без распознанной отметки</option>
          </select>
          <span id="age-filter-help" class="helper"
            >Указано автором; фильтр не проверяет содержание пакета</span
          >
        </label>
        <label
          >Тег<input
            name="tag"
            value={data.filters.tag || ''}
            placeholder="Например: кино"
          /></label
        ><label
          >Размер до<select name="max_bytes" value={data.filters.max_bytes || ''}
            ><option value="">Без ограничения</option><option value="10485760">10 МБ</option><option
              value="104857600">100 МБ</option
            ><option value="262144000">250 МБ</option></select
          ></label
        >
      </div>
    </details>
  </form>
  {#if data.failed}<div class="notice error" role="alert">
      Не удалось загрузить каталог. Проверьте соединение и <a
        class="text-link"
        href={contextLink(`/?${new URLSearchParams(data.filters)}`, room)}>попробуйте ещё раз</a
      >.
    </div>{:else if data.items.length}<div class="card-grid">
      {#each data.items as card}<PackCard {card} />{/each}
    </div>
    <div class="row between section-head">
      {#if Number(data.filters.offset || 0) > 0}<a
          class="button"
          href={contextLink(
            `/?${new URLSearchParams({ ...data.filters, offset: String(Math.max(0, Number(data.filters.offset) - 50)) })}`,
            room
          )}>← Назад</a
        >{/if}{#if data.items.length === 50}<a
          class="button"
          href={contextLink(
            `/?${new URLSearchParams({ ...data.filters, offset: String(Number(data.filters.offset || 0) + 50) })}`,
            room
          )}>Ещё пакеты →</a
        >{/if}
    </div>{:else}<div class="empty">
      <div class="empty-symbol" aria-hidden="true">? / !</div>
      <h3>
        {Object.values(data.filters).some(Boolean)
          ? 'Ничего не нашлось'
          : 'Первый вопрос — за вами'}
      </h3>
      <p>
        {Object.values(data.filters).some(Boolean)
          ? 'Попробуйте другое название или ослабьте фильтры.'
          : 'Пока в каталоге нет опубликованных пакетов. Создайте свой в мастерской и предложите его для публикации.'}
      </p>
      <a
        class="button primary"
        href={contextLink(Object.values(data.filters).some(Boolean) ? '/' : '/studio', room)}
        >{Object.values(data.filters).some(Boolean) ? 'Сбросить фильтры' : 'Открыть мастерскую'}</a
      >
    </div>{/if}
</section>
