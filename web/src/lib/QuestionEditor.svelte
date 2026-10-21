<script lang="ts">
  import type { Question, Draft, Content } from './types';
  import AssetPicker from './AssetPicker.svelte';
  import ContentOptions from './ContentOptions.svelte';
  import { questionTypes } from './game-v2.ts';
  import { setSimpleParameter, setPriceRange } from './question-parameters.ts';
  let {
    question,
    assets = [],
    onchange
  }: {
    question: Question;
    assets: Draft['assets'];
    onchange: (value: Question) => void;
  } = $props();
  const parameter = (name: string) => question.params.find((item) => item.name === name);
  function simple(name: string, value: string) {
    onchange(setSimpleParameter(question, name, value));
  }
  function priceRange(patch: { minimum?: number; maximum?: number; step?: number }) {
    onchange(
      setPriceRange(question, {
        minimum: 0,
        maximum: 0,
        ...parameter('price')?.number_set,
        ...patch
      })
    );
  }
  function info(field: 'comments' | 'sources' | 'showman_comments', value: string) {
    const q = copy();
    q.info = {
      ...q.info,
      [field]: field === 'sources' ? value.split('\n').filter(Boolean) : value
    };
    onchange(q);
  }
  const copy = () => JSON.parse(JSON.stringify(question)) as Question;
  function set(field: 'price' | 'type' | 'right' | 'wrong', value: unknown) {
    const q = copy();
    Object.assign(q, { [field]: value });
    onchange(q);
  }
  function updateItem(index: number, fields: Partial<Content>, group = 'question') {
    const q = copy();
    const p = q.params.find((parameter) => parameter.name === group);
    if (p?.items) {
      Object.assign(p.items[index], fields);
      onchange(q);
    }
  }
  function add(type: string, group = 'question') {
    const q = copy();
    let p = q.params.find((parameter) => parameter.name === group);
    if (!p) {
      p = { name: group, type: 'content', items: [] };
      q.params.push(p);
    }
    p.items ||= [];
    p.items.push({ type, value: '', ...(type !== 'text' ? { is_ref: true } : {}) });
    onchange(q);
  }
  function remove(index: number, group = 'question') {
    const q = copy();
    q.params.find((parameter) => parameter.name === group)?.items?.splice(index, 1);
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
        value={question.type || ''}
        onchange={(event) => set('type', event.currentTarget.value)}
      >
        <option value="">По типу раунда</option
        >{#each Object.entries(questionTypes) as [type, label]}<option value={type}>{label}</option
          >{/each}{#if question.type && !questionTypes[question.type]}<option value={question.type}
            >{question.type}</option
          >{/if}</select
      ></label
    >
  </div>
  <details>
    <summary>Параметры вопроса</summary>
    <div class="stack">
      <label
        >Время на вопрос, секунд<input
          type="number"
          min="5"
          max="120"
          value={parameter('answerDuration')?.value || ''}
          onchange={(event) => simple('answerDuration', event.currentTarget.value)}
          placeholder="По правилам комнаты"
        /><span class="helper"
          >От 5 до 120 секунд. Для обычного вопроса — окно кнопки; ответ после нажатия идёт по
          правилам комнаты.</span
        ></label
      >
      {#if question.type === 'secret' || ['theme', 'price', 'selectionMode'].some( (name) => parameter(name) )}<label
          >Тема секрета<input
            value={parameter('theme')?.value || ''}
            onchange={(event) => simple('theme', event.currentTarget.value)}
            placeholder="Тема из пакета"
          /></label
        ><label
          >Получатель<select
            value={parameter('selectionMode')?.value || ''}
            onchange={(event) => simple('selectionMode', event.currentTarget.value)}
            ><option value="">По умолчанию</option><option value="any">Можно оставить себе</option
            ><option value="exceptCurrent">Только другой игрок</option></select
          ></label
        ><label class="check"
          ><input
            type="checkbox"
            checked={!!parameter('price')}
            onchange={(event) =>
              onchange(
                setPriceRange(
                  question,
                  event.currentTarget.checked ? { minimum: 100, maximum: 500 } : null
                )
              )}
          />Предложить выбор стоимости</label
        >{#if parameter('price')}<div class="form-grid">
            <label
              >Минимум<input
                type="number"
                min="0"
                max="100000"
                value={parameter('price')?.number_set?.minimum ?? 0}
                onchange={(event) => priceRange({ minimum: Number(event.currentTarget.value) })}
              /></label
            ><label
              >Максимум<input
                type="number"
                min="0"
                max="100000"
                value={parameter('price')?.number_set?.maximum ?? 0}
                onchange={(event) => priceRange({ maximum: Number(event.currentTarget.value) })}
              /></label
            ><label
              >Шаг<input
                type="number"
                min="0"
                max="100000"
                value={parameter('price')?.number_set?.step ?? ''}
                onchange={(event) =>
                  priceRange({
                    step:
                      event.currentTarget.value === ''
                        ? undefined
                        : Number(event.currentTarget.value)
                  })}
              /><span class="helper"
                >Пусто или 0 — любое целое. Положительный шаг задаёт сетку; максимум доступен
                всегда.</span
              ></label
            >
          </div>{/if}{/if}
      <label
        >Формат ответа<select
          value={parameter('answerType')?.value || ''}
          onchange={(event) => simple('answerType', event.currentTarget.value)}
          ><option value="">По умолчанию</option><option value="text">Текст</option></select
        ></label
      >
    </div>
  </details>
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
              rows="5"></textarea>{:else}<AssetPicker
              {assets}
              {item}
              label={`Файл вопроса, фрагмент ${index + 1}`}
              onchange={(patch) => updateItem(index, patch)}
            />{/if}
          <ContentOptions {item} onchange={(patch) => updateItem(index, patch)} />
        </div>{/each}
      <div class="row">
        <button type="button" class="small" onclick={() => add('text')}>＋ Текст</button><button
          type="button"
          class="small"
          onclick={() => add('image')}>＋ Изображение</button
        ><button type="button" class="small" onclick={() => add('audio')}>＋ Аудио</button><button
          type="button"
          class="small"
          onclick={() => add('video')}>＋ Видео</button
        >
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
  <details>
    <summary>Пояснение и источники</summary>
    <div class="stack">
      <fieldset>
        <legend>Фрагменты пояснения к ответу</legend>
        <div class="stack">
          {#each parameter('answer')?.items || [] as item, index}<div class="stack gap-sm">
              <div class="row between">
                <span class="helper"
                  >Пояснение {index + 1} · {item.type === 'text' ? 'текст' : item.type}</span
                ><button type="button" class="small ghost" onclick={() => remove(index, 'answer')}
                  >Удалить</button
                >
              </div>
              {#if item.type === 'text'}<textarea
                  value={item.value}
                  oninput={(event) =>
                    updateItem(index, { value: event.currentTarget.value }, 'answer')}
                  aria-label={`Пояснение, фрагмент ${index + 1}`}></textarea><ContentOptions
                  {item}
                  onchange={(patch) => updateItem(index, patch, 'answer')}
                />{:else}<AssetPicker
                  {assets}
                  {item}
                  label={`Файл пояснения, фрагмент ${index + 1}`}
                  onchange={(patch) => updateItem(index, patch, 'answer')}
                /><ContentOptions
                  {item}
                  onchange={(patch) => updateItem(index, patch, 'answer')}
                />{/if}
            </div>{/each}<button type="button" class="small" onclick={() => add('text', 'answer')}
            >＋ Фрагмент пояснения</button
          >
          <div class="row">
            <button type="button" class="small" onclick={() => add('image', 'answer')}
              >＋ Изображение в ответ</button
            ><button type="button" class="small" onclick={() => add('audio', 'answer')}
              >＋ Аудио в ответ</button
            ><button type="button" class="small" onclick={() => add('video', 'answer')}
              >＋ Видео в ответ</button
            >
          </div>
          <p class="helper">
            Фрагменты раскрываются последовательно после показа правильного ответа.
          </p>
        </div>
      </fieldset>
      <label
        >Источники — каждый с новой строки<textarea
          value={(question.info.sources || []).join('\n')}
          onchange={(event) => info('sources', event.currentTarget.value)}></textarea></label
      ><label
        >Комментарий<textarea
          value={question.info.comments || ''}
          onchange={(event) => info('comments', event.currentTarget.value)}></textarea></label
      ><label
        >Комментарий ведущему<textarea
          value={question.info.showman_comments || ''}
          onchange={(event) => info('showman_comments', event.currentTarget.value)}
        ></textarea></label
      >
    </div>
  </details>
</div>
