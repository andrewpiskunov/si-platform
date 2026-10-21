<script lang="ts">
  import { untrack } from 'svelte';
  import {
    actionFor,
    classicRules,
    prepareAction,
    type SessionView,
    type V2Command
  } from './game-v2.ts';
  import type { RoomState } from './room-client.ts';
  import {
    RulesVersionObserver,
    createForm,
    editForm,
    observeForm,
    resetForm,
    submitForm,
    normalizedSettings,
    validRoomSettings
  } from './room-preferences.ts';
  import { errorText } from './api.ts';
  import GameRules from './GameRules.svelte';
  import RoomSettingsFields from './RoomSettingsFields.svelte';
  import RulesSummary from './RulesSummary.svelte';
  import VoiceLink from './VoiceLink.svelte';
  let {
    room,
    view,
    ready,
    pending,
    onsend
  }: {
    room: RoomState;
    view: SessionView;
    ready: boolean;
    pending: number;
    onsend: (command: V2Command) => void;
  } = $props();
  let rulesForm = $state(createForm(classicRules())),
    settingsForm = $state(createForm(normalizedSettings())),
    notice = $state(''),
    error = $state('');
  let identity = '',
    observer: RulesVersionObserver | undefined;
  const settings = $derived(normalizedSettings(room.settings));
  const blocked = $derived(!ready || pending > 0);
  $effect(() => {
    const key = `${room.room_id}:${room.you}`,
      rules = view.rules,
      version = view.rules_version,
      currentSettings = settings;
    untrack(() => {
      if (identity !== key) {
        identity = key;
        observer = new RulesVersionObserver(key);
        rulesForm = createForm(rules);
        settingsForm = createForm(currentSettings);
        notice = '';
        error = '';
      } else {
        rulesForm = observeForm(rulesForm, rules);
        settingsForm = observeForm(settingsForm, currentSettings);
      }
      if (observer?.observe(version))
        notice = `Правила изменены: версия ${version}. Новые правила действуют со следующего вопроса.`;
    });
  });
  function apply(type: 'set_rules' | 'set_room_settings') {
    error = '';
    try {
      const payload =
        type === 'set_rules' ? { rules: rulesForm.value } : { settings: settingsForm.value };
      const command = prepareAction(view, room.seq, type, payload);
      if (type === 'set_rules') rulesForm = submitForm(rulesForm);
      else settingsForm = submitForm(settingsForm);
      onsend(command);
    } catch (e) {
      error = errorText(e);
    }
  }
</script>

<section class="stack room-preferences" aria-label="Правила и доступ">
  {#if notice}<div class="notice rules-notice" role="status" aria-live="polite">
      <p>{notice}</p>
      <button class="small" aria-label="Скрыть уведомление о правилах" onclick={() => (notice = '')}
        >Понятно</button
      >
    </div>{/if}
  <p class="helper access-status">
    Доступ: {settings.visibility === 'public' ? 'Публичная комната' : 'По приглашению'}
  </p>
  <VoiceLink value={settings.voice_url} />
  <details>
    <summary>Текущие правила · версия {view.rules_version}</summary><RulesSummary
      rules={view.rules}
    />
  </details>
  {#if actionFor(view, 'set_rules')}<details>
      <summary>Правила игры</summary>
      <form
        class="stack"
        onsubmit={(event) => {
          event.preventDefault();
          apply('set_rules');
        }}
      >
        {#if rulesForm.conflict}<p class="notice" role="status">
            Правила изменились в комнате. Ваши несохранённые правки сохранены. Сбросьте их или явно
            примените свой вариант.
          </p>{/if}
        <GameRules
          rules={rulesForm.value}
          onchange={(value) => (rulesForm = editForm(rulesForm, value))}
          disabled={blocked}
        />
        <div class="row">
          <button disabled={blocked || !rulesForm.dirty}>Применить правила</button>
          <button
            type="button"
            class="small"
            disabled={!rulesForm.dirty || blocked}
            onclick={() => (rulesForm = resetForm(rulesForm))}>Сбросить изменения правил</button
          >
        </div>
      </form>
    </details>{/if}
  {#if actionFor(view, 'set_room_settings')}<details>
      <summary>Настройки комнаты</summary>
      <form
        class="stack"
        onsubmit={(event) => {
          event.preventDefault();
          apply('set_room_settings');
        }}
      >
        {#if settingsForm.conflict}<p class="notice" role="status">
            Настройки изменились в комнате. Ваши несохранённые правки сохранены. Сбросьте их или
            явно примените свой вариант.
          </p>{/if}
        <RoomSettingsFields
          settings={settingsForm.value}
          onchange={(value) => (settingsForm = editForm(settingsForm, value))}
          disabled={blocked}
        />
        {#if settings.visibility === 'public' && settingsForm.value.visibility === 'private'}<p
            class="notice"
          >
            Свободный вход закроется, прежние приглашения перестанут действовать. Уже вошедшие
            участники останутся. После сохранения создайте новое приглашение.
          </p>{/if}
        <div class="row">
          <button
            disabled={blocked || !settingsForm.dirty || !validRoomSettings(settingsForm.value)}
            >Сохранить настройки комнаты</button
          >
          <button
            type="button"
            class="small"
            disabled={!settingsForm.dirty || blocked}
            onclick={() => (settingsForm = resetForm(settingsForm))}
            >Сбросить изменения настроек</button
          >
        </div>
      </form>
    </details>{/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
</section>

<style>
  .access-status {
    margin: 0;
  }
  .rules-notice p {
    margin-bottom: 12px;
  }
</style>
