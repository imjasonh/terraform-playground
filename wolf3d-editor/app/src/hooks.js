import { useSyncExternalStore } from 'react';
import { subscribe, getVersion } from './store.js';

/** Re-render the component whenever the store changes. */
export function useStoreVersion() {
  return useSyncExternalStore(subscribe, getVersion);
}
