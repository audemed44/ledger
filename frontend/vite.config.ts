import preact from '@preact/preset-vite';
import { defineConfig } from 'vitest/config';
export default defineConfig({plugins:[preact()],build:{outDir:'../web/dist',emptyOutDir:true,assetsInlineLimit:0},server:{proxy:{'/api':{target:'http://localhost:8080',changeOrigin:false}}},test:{environment:'jsdom'}});
