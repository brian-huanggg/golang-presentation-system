import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  srcDir: "articles",
  
  title: "site",
  description: "A VitePress Site",
  themeConfig: {
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: 'Home', link: '/' },
      { text: 'Features', link: '/platform-foundation' }
    ],

    sidebar: [
      {
        text: 'Features',
        items: [
          { text: 'Platform 基礎設施', link: '/platform-foundation' },
          { text: 'Audit 套件', link: '/audit-recorder' },
          { text: 'Event Policy 截止時間', link: '/event-policy' },
          { text: 'DB Migration v1', link: '/db-migrations-v1' },
          { text: '上傳管線 Spike', link: '/upload-pipeline' },
          { text: '註冊 API 與表單', link: '/registration' },
          { text: '密碼政策 Validator', link: '/password-validator' },
          { text: '登入、登出與 Session', link: '/login-session' },
          { text: '帳號鎖定與通用錯誤訊息', link: '/account-lockout' },
          { text: '登入 IP 限流 Middleware', link: '/ip-rate-limit' },
          { text: 'CSRF 防護', link: '/csrf' },
          { text: 'Admin Vault Seed 與強制改密碼', link: '/vault-admin-seed' }
        ]
      }
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/vuejs/vitepress' }
    ]
  }
})
