import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'

import App from './App.vue'
import { setPortalUnauthorizedHandler, setUnauthorizedHandler } from './api/client'
import router from './router'
import { ADMIN_LOGIN, PORTAL_LOGIN, PORTAL_ROOT } from './router/paths'
import { pinia } from './stores'
import { useAuthStore } from './stores/auth'
import { usePortalAuthStore } from './stores/portalAuth'
import './style.css'

const app = createApp(App)
app.use(pinia)
app.use(router)
app.use(ElementPlus, { locale: zhCn })

setUnauthorizedHandler(() => {
  const auth = useAuthStore(pinia)
  auth.clear()
  const current = router.currentRoute.value
  // While the initial navigation is unresolved the router still reports
  // START_LOCATION ("/"); the route guard owns that redirect. Login probes
  // also return 401 by design and must not record themselves as the target.
  if (current.matched.length === 0 || current.meta.public) return
  if (current.path !== ADMIN_LOGIN) {
    void router.replace({
      path: ADMIN_LOGIN,
      query:
        current.fullPath === '/admin' ? undefined : { redirect: current.fullPath },
    })
  }
})

setPortalUnauthorizedHandler(() => {
  const portal = usePortalAuthStore(pinia)
  portal.clear()
  const current = router.currentRoute.value
  if (current.matched.length === 0 || current.meta.portalPublic) return
  if (current.path !== PORTAL_LOGIN) {
    void router.replace({
      path: PORTAL_LOGIN,
      query:
        current.fullPath === PORTAL_ROOT
          ? undefined
          : { redirect: current.fullPath },
    })
  }
})

app.mount('#app')
