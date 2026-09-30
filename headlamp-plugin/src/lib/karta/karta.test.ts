// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { afterEach, beforeAll, describe, expect, it, type MockInstance,vi } from 'vitest';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({ ApiProxy: { request } }));

import { getKartaWasm, loadScriptViaApiProxy } from './karta';

const PATH = '/plugins/karta/wasm_exec.js';

// Waits for loadScriptViaApiProxy to inject its <script> tag, then returns
// it so a test can drive script.onload/onerror itself — jsdom never
// actually executes blob: scripts, so nothing does this automatically.
async function getInjectedScript(appendChild: MockInstance): Promise<HTMLScriptElement> {
  await vi.waitFor(() => expect(appendChild).toHaveBeenCalled());
  return appendChild.mock.calls[0][0] as HTMLScriptElement;
}

describe('loadScriptViaApiProxy', () => {
  beforeAll(() => {
    // jsdom does not implement the Blob-URL APIs at all; vi.spyOn requires
    // the property to exist first, so stub it before any test can spy on it.
    if (!URL.createObjectURL) URL.createObjectURL = vi.fn();
    if (!URL.revokeObjectURL) URL.revokeObjectURL = vi.fn();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.head.innerHTML = '';
  });

  it('fetches the script through ApiProxy.request and runs it via a blob: script tag', async () => {
    const blobUrl = 'blob:http://localhost/00000000-0000-0000-0000-000000000000';
    request.mockResolvedValue({ text: () => Promise.resolve('window.__loaded = true;') });
    const createObjectURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue(blobUrl);
    const revokeObjectURL = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const appendChild = vi.spyOn(document.head, 'appendChild');

    const loaded = loadScriptViaApiProxy(PATH);
    const script = await getInjectedScript(appendChild);
    expect(script.src).toBe(blobUrl);
    script.onload?.(new Event('load'));
    await loaded;

    expect(request).toHaveBeenCalledWith(PATH, { isJSON: false }, false, false);
    expect(createObjectURL).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'application/javascript' })
    );
    expect(revokeObjectURL).toHaveBeenCalledWith(blobUrl);
  });

  it('rejects when the injected script tag fails to load', async () => {
    request.mockResolvedValue({ text: () => Promise.resolve('') });
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:http://localhost/fake');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const appendChild = vi.spyOn(document.head, 'appendChild');

    const loaded = loadScriptViaApiProxy(PATH);
    const script = await getInjectedScript(appendChild);
    script.onerror?.(new Event('error'));

    await expect(loaded).rejects.toThrow('failed to load');
  });
});

describe('getKartaWasm', () => {
  beforeAll(() => {
    if (!URL.createObjectURL) URL.createObjectURL = vi.fn();
    if (!URL.revokeObjectURL) URL.revokeObjectURL = vi.fn();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.head.innerHTML = '';
    delete (window as Partial<Window>).Go;
    delete (window as Partial<Window>).karta;
  });

  // Drives instantiate() up to (but not through) WebAssembly.instantiateStreaming:
  // jsdom never executes injected blob: script content (see getInjectedScript's
  // comment above), so the Go runtime is stubbed directly rather than relying on
  // wasm_exec.js actually running.
  async function driveToInstantiateStreaming(runSpy: (instance: WebAssembly.Instance) => Promise<void>) {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:http://localhost/wasm-exec');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    request.mockImplementation((path: string) => {
      if (path === '/plugins') return Promise.resolve([]);
      if (path.endsWith('wasm_exec.js')) return Promise.resolve({ text: () => Promise.resolve('') });
      if (path.endsWith('karta.wasm')) return Promise.resolve({} as Response);
      return Promise.reject(new Error(`unexpected request: ${path}`));
    });
    const appendChild = vi.spyOn(document.head, 'appendChild');

    const promise = getKartaWasm();
    const script = await getInjectedScript(appendChild);
    window.Go = class {
      importObject: WebAssembly.Imports = {};
      run(instance: WebAssembly.Instance) {
        return runSpy(instance);
      }
    } as unknown as new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): Promise<void> };
    script.onload?.(new Event('load'));

    return promise;
  }

  it('logs and rejects when instantiateStreaming fails, without ever calling go.run', async () => {
    const instantiateError = new Error('bad content type');
    vi.spyOn(WebAssembly, 'instantiateStreaming').mockRejectedValue(instantiateError);
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const runSpy = vi.fn<(instance: WebAssembly.Instance) => Promise<void>>();

    const karta = driveToInstantiateStreaming(runSpy);

    await expect(karta).rejects.toThrow('bad content type');
    expect(consoleError).toHaveBeenCalledWith('karta: failed to instantiate the WASM module', instantiateError);
    expect(runSpy).not.toHaveBeenCalled();
  });
});
