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
          { text: 'DB Migration v1', link: '/db-migrations-v1' }
          { text: '上傳管線 Spike', link: '/upload-pipeline' }
        ]
      }
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/vuejs/vitepress' }
    ]
  }
})
