<script lang="ts">
  import { page } from '$app/state';
</script>

<section class="empty page narrow">
  <p class="eyebrow">{page.status}</p>
  <h1>Страница недоступна</h1>
  <p>Проверьте ссылку или вернитесь в каталог.</p>
  <a class="button primary" href="/">В каталог</a>
</section>
