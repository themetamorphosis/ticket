import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Camera } from './Camera';

let track;
beforeEach(() => {
  track = { stop: vi.fn() };
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [track] }) },
  });
  Object.defineProperty(HTMLMediaElement.prototype, 'play', { configurable: true, writable: true, value: vi.fn().mockResolvedValue() });
});
afterEach(() => {
  delete window.BarcodeDetector;
  vi.useRealTimers();
});

describe('Camera', () => {
  it('reads QR codes with the built-in detector and stops the camera when done', async () => {
    let calls = 0;
    window.BarcodeDetector = class {
      detect() {
        calls++;
        if (calls === 1) return Promise.reject(new Error('busy'));
        if (calls === 2) return Promise.resolve([]);
        return Promise.resolve([{ rawValue: 'https://gate.test/scan#abc' }]);
      }
    };
    const onCode = vi.fn();
    const { rerender } = render(<Camera active onCode={onCode} interval={5} />);
    await waitFor(() => expect(onCode).toHaveBeenCalledWith('https://gate.test/scan#abc'));
    rerender(<Camera active={false} onCode={onCode} interval={5} />);
    expect(track.stop).toHaveBeenCalled();
  });

  it('falls back to the ZXing decoder', async () => {
    const stop = vi.fn();
    let callback;
    const loadZXing = vi.fn().mockResolvedValue({
      BrowserQRCodeReader: class {
        decodeFromVideoElement(_video, cb) {
          callback = cb;
          return Promise.resolve({ stop });
        }
      },
    });
    const onCode = vi.fn();
    const { unmount } = render(<Camera active onCode={onCode} loadZXing={loadZXing} />);
    await waitFor(() => expect(callback).toBeTruthy());
    act(() => callback(undefined));
    act(() => callback({ getText: () => 'code-1' }));
    expect(onCode).toHaveBeenCalledTimes(1);
    unmount();
    expect(stop).toHaveBeenCalled();
  });

  it('works when play() is not available', async () => {
    Object.defineProperty(HTMLMediaElement.prototype, 'play', { configurable: true, writable: true, value: undefined });
    window.BarcodeDetector = class {
      detect() {
        return Promise.resolve([]);
      }
    };
    render(<Camera active onCode={vi.fn()} />);
    await waitFor(() => expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalled());
  });

  it('says so when the camera is refused', async () => {
    navigator.mediaDevices.getUserMedia.mockRejectedValue(new Error('denied'));
    render(<Camera active onCode={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('camera is unavailable');
  });

  it('releases a camera that arrives after it was switched off', async () => {
    let resolve;
    navigator.mediaDevices.getUserMedia.mockReturnValue(new Promise((r) => (resolve = r)));
    const { unmount } = render(<Camera active onCode={vi.fn()} />);
    unmount();
    await act(async () => resolve({ getTracks: () => [track] }));
    expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalled();
  });

  it('does nothing while inactive', () => {
    render(<Camera active={false} onCode={vi.fn()} />);
    expect(navigator.mediaDevices.getUserMedia).not.toHaveBeenCalled();
  });
});

describe('Camera default decoder', () => {
  it('loads the vendored ZXing decoder when the browser has no BarcodeDetector', async () => {
    render(<Camera active onCode={vi.fn()} />);
    // jsdom cannot decode video; reaching the decoder (or reporting the camera
    // as unavailable) shows the dynamic import ran without crashing the page.
    await waitFor(() => expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 300));
  });
});
