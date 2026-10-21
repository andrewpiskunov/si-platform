<script lang="ts">
  import type { RoomSettings } from './types.ts';
  import { voiceDestination } from './room-preferences.ts';
  let {
    settings,
    onchange,
    disabled = false
  }: {
    settings: RoomSettings;
    onchange: (value: RoomSettings) => void;
    disabled?: boolean;
  } = $props();
  const invalidVoice = $derived(!!settings.voice_url && !voiceDestination(settings.voice_url));
</script>

<fieldset class="stack" {disabled}>
  <legend>Доступ и начало игры</legend>
  <label class="row"
    ><input
      type="checkbox"
      checked={!!settings.require_ready}
      onchange={(event) => onchange({ ...settings, require_ready: event.currentTarget.checked })}
    />Ждать готовности всех игроков</label
  >
  <label
    >Доступ в комнату<select
      value={settings.visibility}
      onchange={(event) =>
        onchange({
          ...settings,
          visibility: event.currentTarget.value as RoomSettings['visibility']
        })}
    >
      <option value="private">По приглашению</option><option value="public">Публичная</option>
    </select></label
  >
  <p class="helper">
    {settings.visibility === 'public'
      ? 'Комната появится в списке. Любой посетитель сможет занять доступное место.'
      : 'Новые участники смогут войти только по действующему приглашению.'}
  </p>
  <label
    >Голосовая ссылка (HTTPS)<input
      type="url"
      value={settings.voice_url}
      aria-invalid={invalidVoice}
      placeholder="https://…"
      oninput={(event) => onchange({ ...settings, voice_url: event.currentTarget.value })}
    />
    <span class="helper"
      >Необязательно. Участники откроют ссылку отдельно; сайт не загружает содержимое сервиса.</span
    >
  </label>
  {#if invalidVoice}<p class="notice error">
      Нужна HTTPS-ссылка без логина, пароля и пробелов, до 2048 байт.
    </p>{/if}
</fieldset>
