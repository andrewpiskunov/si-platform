<script lang="ts">
  import type { Question, Draft, Content } from './types';
  let {
    question,
    assets = [],
    onchange
  }: {
    question: Question;
    assets: Draft['assets'];
    onchange: (value: Question) => void;
  } = $props();
  const copy = () => JSON.parse(JSON.stringify(question)) as Question;
  function set(field: 'price' | 'type' | 'right' | 'wrong', value: unknown) {
    const q = copy();
    Object.assign(q, { [field]: value });
    onchange(q);
  }
  function updateItem(index: number, fields: Partial<Content>) {
    const q = copy();
    const p = q.params.find((parameter) => parameter.name === 'question');
    if (p?.items) {
      Object.assign(p.items[index], fields);
      onchange(q);
    }
  }
  function add(type: string) {
    const q = copy();
    let p = q.params.find((parameter) => parameter.name === 'question');
    if (!p) {
      p = { name: 'question', type: 'content', items: [] };
      q.params.push(p);
    }
    p.items ||= [];
    p.items.push({ type, value: '', ...(type !== 'text' ? { is_ref: true } : {}) });
    onchange(q);
  }
  function remove(index: number) {
    const q = copy();
    q.params.find((parameter) => parameter.name === 'question')?.items?.splice(index, 1);
    onchange(q);
  }
</script>

<div class="editor-form">
  <div class="form-grid">
    <label
      >Стоимость<input
        type="number"
        min="0"
        max="100000"
        value={question.price}
        oninput={(event) => set('price', Number(event.currentTarget.value))}
      /></label
    ><label
      >Тип вопроса<select
        value={question.type || 'simple'}
        onchange={(event) => set('type', event.currentTarget.value)}
        ><option value="simple">Обычный</option><option value="withButton">На кнопку</option
        >{#if question.type && !['simple', 'withButton'].includes(question.type)}<option
            value={question.type}>{question.type}</option
          >{/if}</select
      ></label
    >
  </div>
  <fieldset>
    <legend>Содержимое вопроса</legend>
    <div class="stack">
      {#each question.params.find((parameter) => parameter.name === 'question')?.items || [] as item, index}<div
          class="stack gap-sm"
        >
          <div class="row between">
            <span class="helper"
              >{index + 1}. {(
                { text: 'Текст', image: 'Изображение', audio: 'Аудио', video: 'Видео' } as Record<
                  string,
                  string
                >
              )[item.type] || item.type}</span
            ><button
              type="button"
              class="small ghost"
              onclick={() => remove(index)}
              aria-label={`Удалить фрагмент ${index + 1}`}>Удалить</button
            >
          </div>
          {#if item.type === 'text'}<textarea
              value={item.value}
              oninput={(event) => updateItem(index, { value: event.currentTarget.value })}
              placeholder="Напишите вопрос…"
              aria-label={`Текст вопроса, фрагмент ${index + 1}`}
              rows="5"></textarea>{:else if item.is_ref}<label
              >Файл из пакета<select
                value={item.value}
                onchange={(event) => updateItem(index, { value: event.currentTarget.value })}
                ><option value="">Выберите файл</option
                >{#if item.value && !assets.some((asset) => asset.name === item.value)}<option
                    value={item.value}>{item.value}</option
                  >{/if}{#each assets as asset}<option value={asset.name}>{asset.name}</option
                  >{/each}</select
              ></label
            >{:else}<label
              >Ссылка на медиа<input
                value={item.value}
                oninput={(event) => updateItem(index, { value: event.currentTarget.value })}
              /></label
            >{/if}
        </div>{/each}
      <div class="row">
        <button type="button" class="small" onclick={() => add('text')}>＋ Текст</button
        >{#if assets.length}<button type="button" class="small" onclick={() => add('image')}
            >＋ Изображение</button
          ><button type="button" class="small" onclick={() => add('audio')}>＋ Аудио</button><button
            type="button"
            class="small"
            onclick={() => add('video')}>＋ Видео</button
          >{/if}
      </div>
    </div>
  </fieldset>
  <label
    >Правильные ответы<textarea
      value={question.right.join('\n')}
      oninput={(event) => set('right', event.currentTarget.value.split('\n'))}
      placeholder="Каждый вариант — с новой строки"
      rows="3"></textarea><span class="helper"
      >Ответы видит ведущий; игрокам они откроются после решения.</span
    ></label
  ><label
    >Неправильные ответы<textarea
      value={(question.wrong || []).join('\n')}
      oninput={(event) => set('wrong', event.currentTarget.value.split('\n').filter(Boolean))}
      rows="2"
      placeholder="Необязательно"></textarea></label
  >
</div>
