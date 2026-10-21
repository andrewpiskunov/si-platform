<script lang="ts">
  import ThemeLibrary from '#lib/ThemeLibrary.svelte';
</script>

<svelte:head><title>Мои темы оформления — Своя игра</title></svelte:head>
<section class="page">
  <div class="section-head">
    <p class="eyebrow">Авторская мастерская</p>
    <h1>Свой вечер. Свои цвета.</h1>
    <p class="muted">
      Соберите оформление из палитры и собственных изображений. Версии тем доступны только вам и
      участникам выбранной комнаты.
    </p>
  </div>
  <ThemeLibrary />
</section>
