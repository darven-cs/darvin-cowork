import { createApp } from 'vue';
import App from './App.vue';
import { registerIcons } from './assets/icons';
import { setLang } from './services/i18n';
import './index.css';

async function bootstrap(): Promise<void> {
  try {
    const { locale } = await window.darvin.getLocale();
    setLang(locale);
  } catch (e) {
    console.error('[i18n] failed to load locale, falling back to default zh:', e);
  }
  const app = createApp(App);
  registerIcons(app);
  app.mount('#app');
}

void bootstrap();
