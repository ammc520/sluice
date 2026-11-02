import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'

import App from './App.vue'
import { setUnauthorizedHandler } from './api/client'
import router from './router'
import { pinia } from './stores'
import { useAuthStore } from './stores/auth'
import './style.css'

const app = createApp(App)
app.use(pinia)
app.use(router)
app.use(ElementPlus, { locale: zhCn })

setUnauthorizedHandler(() => {
  const auth = useAuthStore(pinia)
  auth.clear()
  const current = router.currentRoute.value
  if (current.path !== '/login') {
    void router.replace({
      path: '/login',
      query: current.fullPath === '/' ? undefined : { redirect: current.fullPath },
    })
  }
})

app.mount('#app')
