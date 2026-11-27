'use client';

import { useCallback, useEffect, useState } from 'react';

// Loads data with load(); reload() fetches it again after a change.
export function useLoad(load) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const reload = useCallback(() => {
    load()
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((err) => setError(err.message));
  }, [load]);
  useEffect(reload, [reload]);
  return { data, error, reload };
}
