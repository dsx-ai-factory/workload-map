// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { useEffect, useState } from 'react';
import { getKartaWasm, KartaWasm } from '../../lib/karta/karta';

export interface UseKartaWasmResult {
  karta: KartaWasm | null;
  error: Error | null;
  loading: boolean;
}

// attempt re-runs the load when it changes. getKartaWasm clears its cached
// promise on failure, so a later attempt genuinely retries rather than
// resolving the old rejection.
export function useKartaWasm(attempt = 0): UseKartaWasmResult {
  const [karta, setKarta] = useState<KartaWasm | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    let cancelled = false;
    setError(null);

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
  }, [attempt]);

  return { karta, error, loading: !karta && !error };
}
