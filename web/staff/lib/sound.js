// A short tone with each result, so staff hear the outcome in a noisy venue.
// Admit is a high double beep; anything else a low one. Silent if audio is unavailable.
export function beep(ok, Ctx = globalThis.AudioContext) {
  try {
    const ctx = new Ctx();
    const tones = ok ? [880, 1320] : [220];
    tones.forEach((freq, i) => {
      const osc = ctx.createOscillator();
      osc.frequency.value = freq;
      osc.connect(ctx.destination);
      osc.start(ctx.currentTime + i * 0.15);
      osc.stop(ctx.currentTime + i * 0.15 + 0.12);
    });
  } catch {
    // No audio: the colour, icon and text still show the result.
  }
}
