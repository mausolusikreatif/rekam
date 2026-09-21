// Self-hosted fonts (bundled by Vite, no external requests — works offline)
import '@fontsource-variable/fraunces/opsz.css';
import '@fontsource-variable/fraunces/opsz-italic.css';
import '@fontsource-variable/newsreader/index.css';
import '@fontsource-variable/newsreader/standard-italic.css';
import '@fontsource/ibm-plex-mono/400.css';
import '@fontsource/ibm-plex-mono/500.css';

import './global.css';
import App from './App.svelte';

const app = new App({ target: document.getElementById('app') });
export default app;
