'use client';

import { useEffect, useRef, useState } from 'react';

/**
 * Rear camera that reports QR codes while active. Uses the browser's built-in
 * BarcodeDetector where available (Android Chrome) and loads the pinned ZXing
 * decoder only on devices without it.
 */
export function Camera({ active, onCode, loadZXing = () => import('@zxing/browser'), interval = 250 }) {
  const video = useRef(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!active) return undefined;
    let stream = null;
    let timer = null;
    let controls = null;
    let stopped = false;

    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'environment' }, audio: false })
      .then(async (s) => {
        stream = s;
        if (stopped) return;
        video.current.srcObject = s;
        await video.current.play?.();
        if ('BarcodeDetector' in window) {
          const detector = new window.BarcodeDetector({ formats: ['qr_code'] });
          timer = setInterval(async () => {
            const codes = await detector.detect(video.current).catch(() => []);
            if (codes.length > 0) onCode(codes[0].rawValue);
          }, interval);
        } else {
          const { BrowserQRCodeReader } = await loadZXing();
          controls = await new BrowserQRCodeReader().decodeFromVideoElement(video.current, (result) => {
            if (result) onCode(result.getText());
          });
        }
      })
      .catch(() => setError('The camera is unavailable. Allow camera access, or type the code below.'));

    return () => {
      stopped = true;
      clearInterval(timer);
      controls?.stop();
      stream?.getTracks().forEach((t) => t.stop());
    };
  }, [active, onCode, loadZXing, interval]);

  return (
    <div className="camera">
      <video ref={video} muted playsInline aria-label="Camera view" />
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
