<script lang="ts">
 let {src, name, large = false}: {src?: string; name: string; large?: boolean} = $props();
 let failed = $state('');
</script>
{#if src && failed !== src}
 <img class:large class="package-cover" {src} alt={`Обложка: ${name}`} loading="lazy" decoding="async" onerror={() => failed = src || ''} />
{:else}
 <span class="pack-cover-title" aria-hidden="true">{name || 'Новый пакет'}</span>
{/if}
<style>
 .package-cover { position:absolute; inset:0; width:100%; height:100%; object-fit:cover; }
 .package-cover.large {position:static;display:block;aspect-ratio:1;object-fit:contain;background:var(--surface);border-radius:12px;}
</style>
