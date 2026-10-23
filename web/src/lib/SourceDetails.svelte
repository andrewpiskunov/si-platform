<script lang="ts">
 import type {SourceDetail,Card} from './types.ts';
 import {sourceDate,safeSourceLink,sourcePercent} from './source-metadata.ts';
 import {bytes} from './api.ts';
 let {detail,card}: {detail: SourceDetail; card: Card}=$props();
 const m=$derived(detail.metadata), d=$derived(detail.document);
</script>
<div class="source-details stack">
 {#if m.name}
  <section class="source-section">
   <p class="eyebrow">О пакете · SIBrowser</p>
   {#each m.description || [] as paragraph}<p class="description">{paragraph}</p>{/each}
   <div class="source-links">{#each m.links || [] as link}{#if safeSourceLink(link.url)}<a href={link.url} target="_blank" rel="noopener noreferrer">{link.name} ↗</a>{/if}{/each}</div>
   <dl class="source-facts">
    {#if m.published_date}<div><dt>Опубликован на SIBrowser</dt><dd>{sourceDate(m.published_date)}</dd></div>{/if}
    {#if m.downloads != null}<div><dt>Скачиваний на SIBrowser</dt><dd>{m.downloads.toLocaleString('ru-RU')}</dd></div>{/if}
    <div><dt>Исходный файл</dt><dd>{m.filename}{#if m.file_size_label} · {m.file_size_label} на источнике{/if}</dd></div>
    {#if detail.original_bytes}<div><dt>Размер загруженного SIQ</dt><dd>{bytes(detail.original_bytes)}</dd></div>{/if}
    {#if m.question_count != null && m.question_count !== card.question_count}<div><dt>Вопросов на SIBrowser</dt><dd>{m.question_count} · в импортированном файле: {card.question_count}</dd></div>{/if}
   </dl>
   <div class="source-links">{#each m.authors || [] as author}{#if safeSourceLink(author.url)}<a href={author.url} target="_blank" rel="noopener noreferrer">{author.name} ↗</a>{/if}{/each}</div>
   {#if m.tags?.length}<p class="helper">Теги SIBrowser: {m.tags.map(t => t.name).join(', ')}</p>{/if}
  </section>
  <div class="source-stat-columns">
   {#each [{title:'Состав вопросов',items:m.media},{title:'Категории',items:m.categories}] as group}
    {#if group.items?.length}<section class="source-section"><h2>{group.title}</h2><p class="helper">По данным SIBrowser</p>
     {#each group.items as item}<div class="source-percent"><div><span>{item.name}</span><strong>{sourcePercent(item.percent)}</strong></div><meter min="0" max="100" value={item.percent} aria-label={`${item.name}: ${item.percent}%`}></meter></div>{/each}
    </section>{/if}
   {/each}
  </div>
  <p class="helper">Один вопрос может содержать несколько типов медиа. Проценты не обязательно дают 100% в сумме.</p>
 {/if}
 <section class="source-section"><h2>Раунды и темы</h2>
  {#each d.rounds || [] as round}<div class="source-round"><h3>{round.name}</h3><p>{(round.themes || []).map(t => t.name).join(' · ')}</p></div>{/each}
 </section>
 {#if m.rounds?.length}<details class="source-preview"><summary>Показать вопросы — спойлеры</summary><p class="helper">Предпросмотр SIBrowser. Ответы скрыты.</p>
  {#each m.rounds as round}<h3>{round.name}</h3>{#each round.themes || [] as theme}<details><summary>{theme.name}</summary><ol>{#each theme.questions || [] as question}<li>{question}</li>{/each}</ol></details>{/each}{/each}
 </details>{/if}
 <details><summary>Сведения внутри SIQ</summary><dl class="source-facts">
  <div><dt>Название</dt><dd>{d.name}</dd></div>
  {#if d.authors?.length}<div><dt>Авторы</dt><dd>{d.authors.join(', ')}</dd></div>{/if}
  {#if d.date}<div><dt>Дата пакета</dt><dd>{d.date}</dd></div>{/if}
  {#if d.publisher}<div><dt>Издатель</dt><dd>{d.publisher}</dd></div>{/if}
  {#if d.tags?.length}<div><dt>Теги SIQ</dt><dd>{d.tags.join(', ')}</dd></div>{/if}
  {#if d.generator}<div><dt>Создан в</dt><dd>{d.generator}</dd></div>{/if}
 </dl>{#if d.comments}<p class="description">{d.comments}</p>{/if}
 {#each d.sources || [] as source}<p>{#if safeSourceLink(source)}<a href={source} target="_blank" rel="noopener noreferrer">{source} ↗</a>{:else}{source}{/if}</p>{/each}
 {#if d.contact_uri && safeSourceLink(d.contact_uri)}<a href={d.contact_uri} target="_blank" rel="noopener noreferrer">Связаться с автором ↗</a>{/if}
 </details>
 {#if m.name}<section class="source-section"><h2>Первоисточники</h2><div class="source-links">{#each m.originals || [] as link}{#if safeSourceLink(link.url)}<a href={link.url} target="_blank" rel="noopener noreferrer">{link.name} ↗</a>{/if}{/each}</div><p class="helper">Данные SIBrowser обновлены {new Date(detail.updated_at).toLocaleString('ru-RU')}.</p></section>{/if}
</div>
<style>
 .source-section {padding:20px 0;border-top:1px solid var(--line);min-width:0;}
 h2 {font-size:1.25rem;margin:0 0 14px;} h3 {font-size:1rem;}
 .description {white-space:pre-wrap;line-height:1.65;overflow-wrap:anywhere;}
 .source-facts {display:grid;gap:14px;margin:20px 0;} .source-facts div{display:grid;grid-template-columns:minmax(140px,1fr) 2fr;gap:16px;}
 dt{color:var(--muted);}dd{margin:0;overflow-wrap:anywhere;}
 .source-links{display:flex;flex-wrap:wrap;gap:10px 20px;overflow-wrap:anywhere;}
 .source-stat-columns{display:grid;grid-template-columns:1fr 1fr;gap:32px;}
 .source-percent{margin:14px 0;} .source-percent div{display:flex;justify-content:space-between;gap:12px;}
 meter{width:100%;height:9px;display:block;margin-top:6px;accent-color:var(--accent);}
 .source-round{margin:18px 0;} .source-round p{color:var(--muted);line-height:1.6;}
 .source-preview li{padding:10px 0;border-bottom:1px solid var(--line);overflow-wrap:anywhere;line-height:1.6;}
 @media(max-width:600px){.source-stat-columns{grid-template-columns:1fr;gap:0;}.source-facts div{grid-template-columns:1fr;gap:4px;}}
</style>
