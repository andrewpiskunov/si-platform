<script lang="ts">
  let { name }: { name: 'arrow' | 'user' | 'book' } = $props();
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
  {#if name === 'arrow'}<path d="M5 12h14m-6-6 6 6-6 6" />
  {:else if name === 'user'}<circle cx="12" cy="8" r="4" /><path d="M4 21v-2a8 8 0 0 1 16 0v2" />
  {:else}<path
      d="M3 4h6a4 4 0 0 1 3 2 4 4 0 0 1 3-2h6v15h-6a4 4 0 0 0-3 2 4 4 0 0 0-3-2H3ZM12 6v15"
    />{/if}
</svg>
