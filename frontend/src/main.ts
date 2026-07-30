import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './style.css'
import './composables/useTheme' // applies the resolved theme before mount, avoiding a flash of the wrong one

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
