<script lang="ts">
  import { voiceDestination } from './room-preferences.ts';
  let { value }: { value: string } = $props();
  const destination = $derived(voiceDestination(value));
</script>

{#if destination}<a
    class="button voice-link"
    href={destination.href}
    target="_blank"
    rel="noopener noreferrer"
    data-sveltekit-preload-data="off"
    data-sveltekit-preload-code="off">Голосовая связь · {destination.hostname} ↗</a
  >{/if}

<style>
  .voice-link {
    overflow-wrap: anywhere;
    max-width: 100%;
  }
</style>
