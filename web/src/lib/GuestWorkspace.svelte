<script lang="ts">
  let { kind }: { kind: 'studio' | 'themes' } = $props();
  const destination = $derived(kind === 'studio' ? '/studio' : '/themes');
</script>

<section class="guest-workspace" aria-label={kind === 'studio' ? 'Доступ к мастерской' : 'Личная библиотека оформления'}>
  <div class="guest-copy">
    <p class="guest-label">{kind === 'studio' ? 'Ваша библиотека вопросов' : 'Ваша библиотека оформления'}</p>
    <h2>{kind === 'studio' ? 'Войдите, чтобы работать с пакетами' : 'Войдите, чтобы добавить свою тему'}</h2>
    <p>{kind === 'studio' ? 'Создавайте вопросы, импортируйте SIQ и возвращайтесь к сохранённым черновикам. Пакеты будут доступны в вашем аккаунте.' : 'Загружайте оформление в ZIP, проверяйте его перед игрой и выбирайте для своих комнат. Готовые темы доступны при создании комнаты.'}</p>
    <div class="guest-actions"><a class="button primary" href={`/login?next=${destination}`}>Войти</a><a class="button ghost" href={`/login?mode=register&next=${destination}`}>Создать аккаунт</a></div>
    <a class="guest-secondary" href={kind === 'studio' ? '/packs' : '/themes/guide.html'}>{kind === 'studio' ? 'Посмотреть каталог пакетов ↗' : 'Посмотреть формат своей темы ↗'}</a>
  </div>
  {#if kind === 'studio'}<figure><img src="/screenshots/editor.png" alt="Редактор пакета: раунды, темы, вопросы и ответы" width="1440" height="1000" /><figcaption>Редактор пакетов · пример интерфейса</figcaption></figure>
  {:else}<div class="theme-explainer"><span class="guest-label">Что можно настроить</span><div class="palette" aria-label="Пример сочетания цветов"><i></i><i></i><i></i><i></i></div><h3>Палитра и изображения</h3><p>Цвета интерфейса, фон и декоративные элементы. Табло, вопросы и управление остаются на своих местах.</p><span class="guest-label">ZIP → предпросмотр → тема комнаты</span></div>{/if}
</section>
<style>
.guest-workspace{display:grid;grid-template-columns:1fr 1.1fr;gap:50px;align-items:center;border:1px solid var(--line);border-radius:var(--card-radius,2px);padding:36px;background:var(--surface)}.guest-label{font-size:11px;color:var(--muted);margin-bottom:18px}.guest-copy h2{font-size:28px;line-height:1.25;max-width:420px}.guest-copy>p:not(.guest-label){font-size:14px;color:var(--muted);line-height:1.8;max-width:460px}.guest-actions{display:flex;gap:12px;flex-wrap:wrap;margin:24px 0 18px}.guest-secondary{font-size:12px;color:var(--muted);text-decoration:underline;text-underline-offset:4px}.guest-secondary:hover{color:var(--accent)}figure{margin:0;min-width:0}img{width:100%;height:auto;display:block;border:1px solid var(--line);border-radius:var(--card-radius,2px)}figcaption{font-size:10px;color:var(--muted);padding-top:10px}.theme-explainer{border-left:1px solid var(--line);padding:20px 0 20px 40px}.palette{display:flex;gap:10px;margin:24px 0}.palette i{width:56px;height:64px;border:1px solid #ffffff30;border-radius:2px;background:#071b52}.palette i:nth-child(2){background:#194198}.palette i:nth-child(3){background:#ffe175}.palette i:nth-child(4){background:#f5f8ff}.theme-explainer h3{font-size:20px}.theme-explainer p{font-size:14px;color:var(--muted);max-width:380px}@media(max-width:800px){.guest-workspace{grid-template-columns:1fr;gap:28px;padding:24px}.guest-copy h2{font-size:25px}.theme-explainer{border-left:0;border-top:1px solid var(--line);padding:24px 0 0}}
</style>
