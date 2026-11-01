import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const target = process.env.ORBIT_API ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': target, '/install.sh': target, '/download': target } },
})
