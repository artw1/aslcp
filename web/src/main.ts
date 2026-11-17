import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { initTheme } from './lib/theme.svelte';
import { boot } from './lib/store.svelte';

initTheme();
boot();
export default mount(App, { target: document.getElementById('app')! });
