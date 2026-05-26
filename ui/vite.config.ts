import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (id.includes('@monaco-editor') || id.includes('monaco-editor')) return 'monaco'
          if (id.includes('@xyflow')) return 'xyflow'
          if (id.includes('react-dom')) return 'react-dom'
          if (id.includes('react-router')) return 'react-router'
          if (id.includes('@tanstack')) return 'tanstack-query'
          if (
            id.includes('react-markdown') ||
            id.includes('remark') ||
            id.includes('rehype') ||
            id.includes('unified') ||
            id.includes('mdast') ||
            id.includes('hast') ||
            id.includes('micromark') ||
            id.includes('vfile')
          ) return 'markdown'
          if (id.includes('react')) return 'react'
          return 'vendor'
        },
      },
    },
  },
})
