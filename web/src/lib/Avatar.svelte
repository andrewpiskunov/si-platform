<script lang="ts">
  let { identity = '', small = false }: { identity?: string; small?: boolean } = $props();
  // Only a fixed local asset can be selected; names and credentials never enter a URL.
  const portraits = ['anna', 'mikhail', 'elena', 'sasha', 'host'];
  const portrait = $derived.by(() => {
    let hash = 2166136261;
    for (const code of identity) hash = Math.imul(hash ^ code.codePointAt(0)!, 16777619) >>> 0;
    return portraits[hash % portraits.length];
  });
</script>

<img
  class="participant-avatar"
  class:small
  src={`/avatars/${portrait}.svg`}
  alt=""
  width="48"
  height="48"
/>
