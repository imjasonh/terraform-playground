import React from 'react';
import { useStoreVersion } from './hooks.js';
import { store } from './store.js';
import { OpenScreen } from './components/OpenScreen.jsx';
import { EditorShell } from './components/EditorShell.jsx';

export function App() {
  useStoreVersion();
  return store.screen === 'open' ? <OpenScreen /> : <EditorShell />;
}
