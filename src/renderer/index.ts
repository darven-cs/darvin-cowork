import { createApp } from 'vue';
import App from './App.vue';
import { registerIcons } from './assets/icons';
import './index.css';

const app = createApp(App);
registerIcons(app);
app.mount('#app');
