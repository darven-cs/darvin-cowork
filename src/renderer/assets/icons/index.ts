/**
 * SVG 图标资源：
 * - Vite `?raw` import 把 .svg 当字符串内联
 * - B 组（用户导入）的硬编码 `stroke="black"` 在加载时一次替换为 currentColor
 * - 对外暴露 `SVG_SOURCES` map（Icon 组件消费）
 */

import type { App } from 'vue';
import Icon from '../../components/common/Icon.vue';

const modules = import.meta.glob<string>('./*.svg', {
  eager: true,
  query: '?raw',
  import: 'default',
});

function normalize(svg: string): string {
  return svg.replace(/stroke="black"/g, 'stroke="currentColor"');
}

export const SVG_SOURCES: Record<string, string> = Object.fromEntries(
  Object.entries(modules).map(([path, raw]) => {
    const name = path.replace(/^\.\//, '').replace(/\.svg$/, '');
    return [name, normalize(raw)];
  }),
);

export function registerIcons(app: App): void {
  app.component('Icon', Icon);
}
