<script lang="ts">
  let { name }: { name: 'arrow' | 'user' | 'book' | 'eye' | 'eye-off' } = $props();
</script>

<svg
  class="ui-icon"
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.7"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if name === 'eye'}<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z" /><circle cx="12" cy="12" r="3" />
  {:else if name === 'eye-off'}<path d="m3 3 18 18M10.6 5.1A12 12 0 0 1 12 5c6.5 0 10 7 10 7a18 18 0 0 1-3.1 4.1M6.1 6.1A21 21 0 0 0 2 12s3.5 7 10 7a12 12 0 0 0 5.9-1.9M9.9 9.9a3 3 0 0 0 4.2 4.2" />
  {:else if name === 'arrow'}<path d="M5 12h14m-6-6 6 6-6 6" />
  {:else if name === 'user'}<circle cx="12" cy="8" r="4" /><path d="M4 21v-2a8 8 0 0 1 16 0v2" />
  {:else}<path
      d="M3 4h6a4 4 0 0 1 3 2 4 4 0 0 1 3-2h6v15h-6a4 4 0 0 0-3 2 4 4 0 0 0-3-2H3ZM12 6v15"
    />{/if}
</svg>
