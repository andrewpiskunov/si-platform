<script lang="ts">
  import { onMount } from 'svelte';
  let audio: AudioContext | undefined;
  let message = $state('');
  async function testSound() {
    try {
      audio ||= new AudioContext();
      await audio.resume();
      const oscillator = audio.createOscillator(),
        gain = audio.createGain();
      oscillator.type = 'sine';
      oscillator.frequency.value = 440;
      gain.gain.setValueAtTime(0.08, audio.currentTime);
      gain.gain.exponentialRampToValueAtTime(0.001, audio.currentTime + 0.5);
      oscillator.connect(gain).connect(audio.destination);
      oscillator.start();
      oscillator.stop(audio.currentTime + 0.5);
      message = 'Прозвучал короткий сигнал. Если его не слышно, проверьте громкость устройства.';
    } catch {
      message = 'Браузер не разрешил звук. Проверьте разрешение и попробуйте ещё раз.';
    }
  }
  onMount(() => () => {
    void audio?.close();
  });
</script>

<div class="notice">
  <div class="row between">
    <span>Перед игрой проверьте звук</span><button class="small" onclick={testSound}
      >Проверить звук</button
    >
  </div>
  {#if message}<p class="helper" role="status">{message}</p>{/if}
</div>
