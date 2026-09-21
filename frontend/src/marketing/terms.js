import './fonts.js';
import '../global.css';
import Legal from './Legal.svelte';
import html from '../../../docs/legal/terms-of-service.md?html';

export default new Legal({ target: document.getElementById('app'), props: { html } });
