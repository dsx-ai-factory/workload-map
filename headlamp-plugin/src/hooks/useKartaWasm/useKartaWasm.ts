// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { useEffect, useState } from 'react';
import { getKartaWasm, KartaWasm } from '../../lib/karta/karta';

export interface UseKartaWasmResult {
  karta: KartaWasm | null;
  error: Error | null;
  loading: boolean;
}

export function useKartaWasm(): UseKartaWasmResult {
  const [karta, setKarta] = useState<KartaWasm | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    let cancelled = false;

    getKartaWasm()
      .then(loaded => {
        if (!cancelled) {
          setKarta(loaded);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return { karta, error, loading: !karta && !error };
}
